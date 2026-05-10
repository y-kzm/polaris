package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
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

// Target represents a single SNMP-polled router.
// JSON tags match the YAML keys in param.yaml (snmp_targets list).
type Target struct {
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
	Community string `json:"community"`
	// IfName restricts collection to the named internal-segment interface.
	IfName string `json:"ifname,omitempty"`
	// SelfLL is the router's own link-local on the internal interface.
	// Neighbors matching any target's SelfLL are classified as is_router=1.
	SelfLL string `json:"self_ll,omitempty"`
	// Label is the human-readable name written into the neighbor source field.
	// Falls back to Host when empty.
	Label string `json:"label,omitempty"`
}

type neighbor struct {
	IP        string `json:"ip"`
	LLAddr    string `json:"lladdr"`
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	State     string `json:"state"`
	IsRouter  int    `json:"is_router"`
	Source    string `json:"source"`     // hostname of the router that reported this entry
	UpdatedAt string `json:"updated_at"`
}

// parseTargets parses the SNMP_TARGETS environment variable.
//
// Two formats are accepted:
//
//   - JSON array (primary, set by server.sh from param.yaml):
//     [{"host":"r1","port":161,"community":"public"}, ...]
//
//   - Legacy CSV (fallback, for running start.sh directly):
//     "host", "host:community", or "host:port:community" — comma-separated.
//     Note: IPv6 addresses in this format must be specified as hostnames.
func parseTargets(val string) []Target {
	val = strings.TrimSpace(val)
	if val == "" || val == "null" || val == "[]" {
		return defaultTargets()
	}

	// Primary: JSON array produced by server.sh via module_config_json.
	if strings.HasPrefix(val, "[") {
		var targets []Target
		if err := json.Unmarshal([]byte(val), &targets); err == nil {
			var out []Target
			for _, t := range targets {
				if t.Host == "" {
					continue
				}
				if t.Port == 0 {
					t.Port = 161
				}
				if t.Community == "" {
					t.Community = "public"
				}
				out = append(out, t)
			}
			if len(out) > 0 {
				return out
			}
			return defaultTargets()
		}
	}

	// Fallback: legacy CSV "host:port:community".
	var out []Target
	for _, s := range strings.Split(val, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		t := Target{Port: 161, Community: "public"}
		parts := strings.Split(s, ":")
		switch len(parts) {
		case 1:
			t.Host = parts[0]
		case 2:
			t.Host = parts[0]
			if p, err := strconv.ParseUint(parts[1], 10, 16); err == nil {
				t.Port = uint16(p)
			} else {
				t.Community = parts[1]
			}
		default:
			t.Host = parts[0]
			if p, err := strconv.ParseUint(parts[1], 10, 16); err == nil {
				t.Port = uint16(p)
			}
			t.Community = parts[2]
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return defaultTargets()
	}
	return out
}

func defaultTargets() []Target {
	return []Target{{Host: "localhost", Port: 161, Community: "public"}}
}

type collector struct {
	targetsMu sync.RWMutex
	targets   []Target
	mu        sync.RWMutex
	cached    []neighbor
}

func newCollector(targets []Target) *collector {
	return &collector{targets: targets}
}

func (c *collector) run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		c.refresh()
	}
}

// setTargetIface updates (or adds) the internal interface info for a target.
func (c *collector) setTargetIface(host, ifname, selfLL, label string) {
	c.targetsMu.Lock()
	defer c.targetsMu.Unlock()
	for i, t := range c.targets {
		if t.Host == host {
			c.targets[i].IfName = ifname
			c.targets[i].SelfLL = selfLL
			c.targets[i].Label = label
			return
		}
	}
	c.targets = append(c.targets, Target{Host: host, Port: 161, Community: "public", IfName: ifname, SelfLL: selfLL, Label: label})
}

func (c *collector) refresh() {
	c.targetsMu.RLock()
	targets := make([]Target, len(c.targets))
	copy(targets, c.targets)
	c.targetsMu.RUnlock()

	// Build set of all routers' own link-locals so neighbors matching them
	// are correctly promoted to is_router=1 regardless of routing table.
	routerSelfLLs := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.SelfLL != "" {
			routerSelfLLs[t.SelfLL] = struct{}{}
		}
	}

	seen := map[string]neighbor{}
	for _, t := range targets {
		ns, err := collectFrom(t, routerSelfLLs)
		if err != nil {
			log.Printf("snmp %s: %v", t.Host, err)
			continue
		}
		for _, n := range ns {
			if prev, ok := seen[n.IP]; !ok || n.UpdatedAt > prev.UpdatedAt {
				seen[n.IP] = n
			}
		}
	}
	merged := make([]neighbor, 0, len(seen))
	for _, n := range seen {
		merged = append(merged, n)
	}
	c.mu.Lock()
	c.cached = merged
	c.mu.Unlock()
	log.Printf("snmp: refreshed %d neighbor(s) from %d target(s)", len(merged), len(c.targets))
}

func (c *collector) neighbors(ifname string) []neighbor {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if ifname == "" {
		out := make([]neighbor, len(c.cached))
		copy(out, c.cached)
		return out
	}
	out := []neighbor{}
	for _, n := range c.cached {
		if n.IfName == ifname {
			out = append(out, n)
		}
	}
	return out
}

func connectSNMP(t Target) (*gosnmp.GoSNMP, error) {
	g := &gosnmp.GoSNMP{
		Target:         t.Host,
		Port:           t.Port,
		Community:      t.Community,
		Version:        gosnmp.Version2c,
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

func collectFrom(t Target, routerSelfLLs map[string]struct{}) ([]neighbor, error) {
	g, err := connectSNMP(t)
	if err != nil {
		return nil, err
	}
	defer g.Conn.Close()

	ifNames := walkIfNames(g)
	routers := walkIPv6Routers(g)
	all, err := walkNeighborTable(g, ifNames, routers)
	if err != nil {
		return nil, err
	}

	var out []neighbor
	for _, n := range all {
		// Filter to internal interface if registered.
		if t.IfName != "" && n.IfName != t.IfName {
			continue
		}
		// Promote entries whose IP matches any router's known SelfLL.
		if _, isRouter := routerSelfLLs[n.IP]; isRouter {
			n.IsRouter = 1
		}
		if t.Label != "" {
			n.Source = t.Label
		} else {
			n.Source = t.Host
		}
		out = append(out, n)
	}
	return out, nil
}

// walkIPv6Routers collects IPv6 next-hop addresses from inetCidrRouteTable (IP-MIB, RFC 4292).
// The next-hop is encoded in the OID index as:
//
//	{destType}.{destLen}.{destBytes...}.{pfxLen}.{policyLen}.{policyArcs...}.{nhType}.{nhLen}.{nhBytes...}
//
// Returns an empty map if the MIB is not accessible on the target.
func walkIPv6Routers(g *gosnmp.GoSNMP) map[string]struct{} {
	routers := map[string]struct{}{}
	colPrefix := oidInetCidrRoute + ".1."
	_ = g.BulkWalk(oidInetCidrRoute, func(pdu gosnmp.SnmpPDU) error {
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
		if err != nil || ip == nil || ip.IsUnspecified() {
			return nil
		}
		routers[ip.String()] = struct{}{}
		return nil
	})
	return routers
}

// parseInetCidrRouteNextHop extracts the next-hop IPv6 address from an inetCidrRouteTable OID index suffix.
func parseInetCidrRouteNextHop(s string) (net.IP, error) {
	parts := strings.Split(s, ".")
	i := 0
	next := func() (int, bool) {
		if i >= len(parts) {
			return 0, false
		}
		v, err := strconv.Atoi(parts[i])
		i++
		return v, err == nil
	}

	// skip destType
	if _, ok := next(); !ok {
		return nil, fmt.Errorf("short index")
	}
	// skip dest: length + bytes
	destLen, ok := next()
	if !ok {
		return nil, fmt.Errorf("short index")
	}
	i += destLen
	// skip pfxLen
	if _, ok := next(); !ok {
		return nil, fmt.Errorf("short index")
	}
	// skip policy OID: length + arcs
	policyLen, ok := next()
	if !ok {
		return nil, fmt.Errorf("short index")
	}
	i += policyLen
	// read nhType
	nhType, ok := next()
	if !ok {
		return nil, fmt.Errorf("short index")
	}
	if nhType != 2 { // only IPv6 (InetAddressType = 2)
		return nil, nil
	}
	// read nhLen + nhBytes
	nhLen, ok := next()
	if !ok || nhLen != 16 {
		return nil, fmt.Errorf("unexpected nhLen %d", nhLen)
	}
	if i+nhLen > len(parts) {
		return nil, fmt.Errorf("short index")
	}
	b := make([]byte, nhLen)
	for j := range nhLen {
		v, err := strconv.Atoi(parts[i+j])
		if err != nil {
			return nil, err
		}
		b[j] = byte(v)
	}
	return net.IP(b), nil
}

func walkIfNames(g *gosnmp.GoSNMP) map[int]string {
	names := map[int]string{}
	for _, oid := range []string{oidIfName, oidIfDescr} {
		_ = g.BulkWalk(oid, func(pdu gosnmp.SnmpPDU) error {
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
		if len(names) > 0 {
			break
		}
	}
	return names
}

type neighborEntry struct {
	ifIndex int
	ip      net.IP
	mac     []byte
	state   int
}

func walkNeighborTable(g *gosnmp.GoSNMP, ifNames map[int]string, routers map[string]struct{}) ([]neighbor, error) {
	entries := map[string]*neighborEntry{}
	colPrefix := oidNeighborTable + ".1."

	err := g.BulkWalk(oidNeighborTable, func(pdu gosnmp.SnmpPDU) error {
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
		ifIndex, addrType, ip, err := parseNeighborIndex(index)
		if err != nil || addrType != 2 { // 2 = ipv6
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
		return nil, fmt.Errorf("walk ipNetToPhysicalTable: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]neighbor, 0, len(entries))
	for _, e := range entries {
		state := neighborStates[e.state]
		if state == "" {
			state = "UNKNOWN"
		}
		isRouter := 0
		if _, ok := routers[e.ip.String()]; ok {
			isRouter = 1
		}
		out = append(out, neighbor{
			IP:        e.ip.String(),
			LLAddr:    macString(e.mac),
			IfIndex:   e.ifIndex,
			IfName:    ifNames[e.ifIndex],
			State:     state,
			IsRouter:  isRouter,
			UpdatedAt: now,
		})
	}
	return out, nil
}

// parseNeighborIndex parses "{ifIndex}.{addrType}.{addrLen}.{bytes...}" from an OID index suffix.
func parseNeighborIndex(s string) (ifIndex, addrType int, ip net.IP, err error) {
	parts := strings.Split(s, ".")
	if len(parts) < 3 {
		return 0, 0, nil, fmt.Errorf("index too short")
	}
	ifIndex, err = strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	addrType, err = strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	addrLen, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, nil, err
	}
	if len(parts) < 3+addrLen {
		return 0, 0, nil, fmt.Errorf("not enough address bytes: need %d, have %d", addrLen, len(parts)-3)
	}
	b := make([]byte, addrLen)
	for i := range addrLen {
		v, e := strconv.Atoi(parts[3+i])
		if e != nil {
			return 0, 0, nil, e
		}
		b[i] = byte(v)
	}
	return ifIndex, addrType, net.IP(b), nil
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
