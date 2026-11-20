package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// RFC 4293 ipNetToPhysicalTable — covers both IPv4 and IPv6 neighbor/ARP entries.
const (
	oidNeighborTable = "1.3.6.1.2.1.4.35"
	oidIfName        = "1.3.6.1.2.1.31.1.1.1.1" // IF-MIB::ifName
	oidIfDescr       = "1.3.6.1.2.1.2.2.1.2"    // IF-MIB::ifDescr (fallback)
	oidInetCidrRoute = "1.3.6.1.2.1.4.24.7"     // IP-MIB::inetCidrRouteTable (RFC 4292)
)

// InetAddressType values (RFC 4001) that carry an IPv6 address.
const (
	inetTypeIPv6  = 2 // 16 bytes
	inetTypeIPv6z = 4 // 16 bytes + 4-byte zone index
)

// ipNetToPhysicalState enumeration values (RFC 4293).
var neighborStates = map[int]string{
	1: "REACHABLE",
	2: "STALE",
	3: "DELAY",
	4: "PROBE",
	5: "INVALID",
	6: "UNKNOWN",
	7: "INCOMPLETE",
}

// errNotIPv6 marks an index whose address is well-formed but not IPv6.
var errNotIPv6 = errors.New("not an IPv6 address")

// exportableState maps an ipNetToPhysicalState value to its name and
// reports whether a neighbor in that state can receive an RA: its link-layer
// address is resolved. INVALID, UNKNOWN, INCOMPLETE and values outside the
// enumeration are not exported.
func exportableState(state int) (string, bool) {
	switch state {
	case 1, 2, 3, 4:
		return neighborStates[state], true
	}
	return "", false
}

func connectSNMP(ctx context.Context, t Target) (*gosnmp.GoSNMP, error) {
	g := &gosnmp.GoSNMP{
		Target:         t.Host,
		Port:           t.Port,
		Community:      t.Community,
		Version:        gosnmp.Version2c,
		Context:        ctx,
		Timeout:        5 * time.Second,
		Retries:        2,
		MaxOids:        60,
		MaxRepetitions: 20,
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("connect %s:%d: %w", t.Host, t.Port, err)
	}
	return g, nil
}

// collectFrom polls one target. It is the collector's collectFunc.
func collectFrom(ctx context.Context, t Target) ([]neighbor, error) {
	g, err := connectSNMP(ctx, t)
	if err != nil {
		return nil, err
	}
	defer g.Conn.Close()
	// gosnmp honours the context deadline between requests; closing the
	// connection also interrupts a request that is waiting for its reply.
	stop := context.AfterFunc(ctx, func() { g.Conn.Close() })
	defer stop()

	ifNames, err := walkIfNames(g)
	if err != nil {
		// Without names every neighbor would fail the interface filter and
		// the target would look empty, so the poll counts as failed.
		if t.IfName != "" {
			return nil, fmt.Errorf("interface filter %q cannot be applied: %w", t.IfName, err)
		}
		log.Printf("snmp %s: %v (neighbors are exported without interface names)", t.Host, err)
	} else if t.IfName != "" && !hasIfName(ifNames, t.IfName) {
		log.Printf("snmp %s: interface %q is not among the target's %d interface(s); no neighbor will match", t.Host, t.IfName, len(ifNames))
	}

	routers, err := walkIPv6Routers(g)
	if err != nil {
		log.Printf("snmp %s: walk inetCidrRouteTable: %v (is_router not derived from routes)", t.Host, err)
	}

	entries, malformed, err := walkNeighborTable(g)
	if err != nil {
		return nil, err
	}
	if malformed > 0 {
		log.Printf("snmp %s: skipped %d malformed ipNetToPhysicalTable row(s)", t.Host, malformed)
	}
	return exportNeighbors(entries, ifNames, routers, t.IfName, time.Now()), nil
}

func hasIfName(ifNames map[int]string, name string) bool {
	for _, n := range ifNames {
		if n == name {
			return true
		}
	}
	return false
}

// exportNeighbors turns raw table entries into the served form. It keeps
// only entries that can receive an RA (see exportableState) with a non-empty
// link-layer address and, when ifname is set, only those on that interface.
func exportNeighbors(entries []neighborEntry, ifNames map[int]string, routers map[string]struct{}, ifname string, now time.Time) []neighbor {
	updatedAt := now.UTC().Format(time.RFC3339)
	out := make([]neighbor, 0, len(entries))
	for _, e := range entries {
		state, ok := exportableState(e.state)
		if !ok || len(e.mac) == 0 {
			continue
		}
		name := ifNames[e.ifIndex]
		if ifname != "" && name != ifname {
			continue
		}
		isRouter := 0
		if _, ok := routers[e.ip.String()]; ok {
			isRouter = 1
		}
		out = append(out, neighbor{
			IP:        e.ip.String(),
			LLAddr:    macString(e.mac),
			IfIndex:   e.ifIndex,
			IfName:    name,
			State:     state,
			IsRouter:  isRouter,
			UpdatedAt: updatedAt,
		})
	}
	return out
}

// walkIPv6Routers collects IPv6 next-hop addresses from inetCidrRouteTable (IP-MIB, RFC 4292).
// The next-hop is encoded in the OID index as:
//
//	{destType}.{destLen}.{destBytes...}.{pfxLen}.{policyLen}.{policyArcs...}.{nhType}.{nhLen}.{nhBytes...}
//
// Returns an empty map if the MIB is not accessible on the target.
func walkIPv6Routers(g *gosnmp.GoSNMP) (map[string]struct{}, error) {
	routers := map[string]struct{}{}
	colPrefix := oidInetCidrRoute + ".1."
	err := g.BulkWalk(oidInetCidrRoute, func(pdu gosnmp.SnmpPDU) error {
		oid := strings.TrimPrefix(pdu.Name, ".")
		if !strings.HasPrefix(oid, colPrefix) {
			return nil
		}
		rest := oid[len(colPrefix):]
		col, index, ok := strings.Cut(rest, ".")
		if !ok {
			return nil
		}
		// Process only column 7 (inetCidrRouteIfIndex) to avoid parsing the same index multiple times.
		// Columns 1-6 are INDEX fields and not directly accessible; accessible columns start at 7.
		if col != "7" {
			return nil
		}
		ip, err := parseInetCidrRouteNextHop(index)
		if err != nil || ip.IsUnspecified() {
			return nil
		}
		routers[ip.String()] = struct{}{}
		return nil
	})
	return routers, err
}

// parseInetCidrRouteNextHop extracts the next-hop IPv6 address from an
// inetCidrRouteTable OID index suffix. It returns errNotIPv6 for a next hop
// of another address type.
func parseInetCidrRouteNextHop(s string) (net.IP, error) {
	parts := strings.Split(s, ".")
	i := 0
	// next reads one sub-identifier used as a type or a length.
	next := func() (int, error) {
		if i >= len(parts) {
			return 0, fmt.Errorf("short index")
		}
		v, err := strconv.ParseUint(parts[i], 10, 16)
		i++
		if err != nil {
			return 0, fmt.Errorf("bad sub-identifier %q", parts[i-1])
		}
		return int(v), nil
	}
	// skip advances over n sub-identifiers.
	skip := func(n int) error {
		if n > len(parts)-i {
			return fmt.Errorf("short index")
		}
		i += n
		return nil
	}

	// skip destType
	if _, err := next(); err != nil {
		return nil, err
	}
	// skip dest: length + bytes
	destLen, err := next()
	if err != nil {
		return nil, err
	}
	if err := skip(destLen); err != nil {
		return nil, err
	}
	// skip pfxLen
	if _, err := next(); err != nil {
		return nil, err
	}
	// skip policy OID: length + arcs
	policyLen, err := next()
	if err != nil {
		return nil, err
	}
	if err := skip(policyLen); err != nil {
		return nil, err
	}
	nhType, err := next()
	if err != nil {
		return nil, err
	}
	nhLen, err := next()
	if err != nil {
		return nil, err
	}
	if nhLen != len(parts)-i {
		return nil, fmt.Errorf("next hop length %d, but %d sub-identifier(s) follow", nhLen, len(parts)-i)
	}
	b, err := oidBytes(parts[i:])
	if err != nil {
		return nil, err
	}
	return inetIPv6(nhType, b)
}

// walkIfNames returns ifIndex → name from IF-MIB::ifName, falling back to
// ifDescr. It returns an error when neither walk yields a name.
func walkIfNames(g *gosnmp.GoSNMP) (map[int]string, error) {
	names := map[int]string{}
	var walkErr error
	for _, oid := range []string{oidIfName, oidIfDescr} {
		err := g.BulkWalk(oid, func(pdu gosnmp.SnmpPDU) error {
			idx := lastOIDComponent(pdu.Name)
			if idx <= 0 {
				return nil
			}
			if _, exists := names[idx]; exists {
				return nil
			}
			if v, ok := pdu.Value.([]byte); ok && len(v) > 0 {
				names[idx] = string(v)
			}
			return nil
		})
		if err != nil {
			// A partial walk would make the interface filter drop the
			// neighbors of the interfaces it missed.
			walkErr = errors.Join(walkErr, fmt.Errorf("walk %s: %w", oid, err))
			clear(names)
			continue
		}
		if len(names) > 0 {
			return names, nil
		}
	}
	if walkErr != nil {
		return names, fmt.Errorf("interface names: %w", walkErr)
	}
	return names, errors.New("interface names: target returned neither ifName nor ifDescr")
}

type neighborEntry struct {
	ifIndex int
	ip      net.IP
	mac     []byte
	state   int
}

// walkNeighborTable reads the IPv6 rows of ipNetToPhysicalTable. malformed
// counts the rows skipped because their index could not be decoded.
func walkNeighborTable(g *gosnmp.GoSNMP) (out []neighborEntry, malformed int, err error) {
	entries := map[string]*neighborEntry{}
	bad := map[string]struct{}{}
	colPrefix := oidNeighborTable + ".1."

	err = g.BulkWalk(oidNeighborTable, func(pdu gosnmp.SnmpPDU) error {
		oid := strings.TrimPrefix(pdu.Name, ".")
		if !strings.HasPrefix(oid, colPrefix) {
			return nil
		}
		rest := oid[len(colPrefix):]
		colStr, index, ok := strings.Cut(rest, ".")
		if !ok {
			return nil
		}
		col, err := strconv.Atoi(colStr)
		if err != nil {
			return nil
		}
		ifIndex, ip, err := parseNeighborIndex(index)
		if err != nil {
			if !errors.Is(err, errNotIPv6) {
				bad[index] = struct{}{}
			}
			return nil
		}
		key := fmt.Sprintf("%d/%s", ifIndex, ip.String())
		e := entries[key]
		if e == nil {
			e = &neighborEntry{ifIndex: ifIndex, ip: ip}
			entries[key] = e
		}
		switch col {
		case 4: // ipNetToPhysicalPhysAddress
			if v, ok := pdu.Value.([]byte); ok {
				e.mac = v
			}
		case 7: // ipNetToPhysicalState
			if v, ok := pdu.Value.(int); ok {
				e.state = v
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("walk ipNetToPhysicalTable: %w", err)
	}

	out = make([]neighborEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, *e)
	}
	return out, len(bad), nil
}

// parseNeighborIndex parses "{ifIndex}.{addrType}.{addrLen}.{bytes...}" from
// an ipNetToPhysicalTable OID index suffix. It returns errNotIPv6 for a
// well-formed index of another address type.
func parseNeighborIndex(s string) (ifIndex int, ip net.IP, err error) {
	parts := strings.Split(s, ".")
	if len(parts) < 3 {
		return 0, nil, fmt.Errorf("index too short")
	}
	idx, err := strconv.ParseUint(parts[0], 10, 31)
	if err != nil || idx == 0 {
		return 0, nil, fmt.Errorf("bad ifIndex %q", parts[0])
	}
	addrType, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return 0, nil, fmt.Errorf("bad address type %q", parts[1])
	}
	addrLen, err := strconv.ParseUint(parts[2], 10, 16)
	if err != nil {
		return 0, nil, fmt.Errorf("bad address length %q", parts[2])
	}
	if int(addrLen) != len(parts)-3 {
		return 0, nil, fmt.Errorf("address length %d, but %d sub-identifier(s) follow", addrLen, len(parts)-3)
	}
	b, err := oidBytes(parts[3:])
	if err != nil {
		return 0, nil, err
	}
	ip, err = inetIPv6(int(addrType), b)
	if err != nil {
		return 0, nil, err
	}
	return int(idx), ip, nil
}

// oidBytes converts OID sub-identifiers that encode an octet string to
// bytes. A sub-identifier outside 0..255 is an error, not truncated.
func oidBytes(parts []string) ([]byte, error) {
	b := make([]byte, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("sub-identifier %q is not an octet", p)
		}
		b[i] = byte(v)
	}
	return b, nil
}

// inetIPv6 decodes an InetAddress of type ipv6(2) (16 bytes) or ipv6z(4)
// (20 bytes; the 4-byte zone index is dropped). Other types yield errNotIPv6.
func inetIPv6(addrType int, b []byte) (net.IP, error) {
	var want int
	switch addrType {
	case inetTypeIPv6:
		want = 16
	case inetTypeIPv6z:
		want = 20
	default:
		return nil, errNotIPv6
	}
	if len(b) != want {
		return nil, fmt.Errorf("InetAddressType %d needs %d bytes, got %d", addrType, want, len(b))
	}
	return net.IP(b[:16:16]), nil
}

func lastOIDComponent(oid string) int {
	oid = strings.TrimPrefix(oid, ".")
	last := oid[strings.LastIndex(oid, ".")+1:]
	n, err := strconv.Atoi(last)
	if err != nil {
		return -1
	}
	return n
}

func macString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = hex.EncodeToString([]byte{v})
	}
	return strings.Join(parts, ":")
}
