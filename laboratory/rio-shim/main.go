// rio-shim installs the routes of the Route Information Options (RFC 4191)
// found in received Router Advertisements.
//
// It exists only for kernels built without CONFIG_IPV6_ROUTE_INFO, such as
// the VM kernel of Docker Desktop, which silently ignore the option. On such
// a kernel the lab hosts run this program so that the lab still works; on a
// kernel that supports the option (most Linux distributions) it is not
// started and the kernel does the same job.
//
// Everything else in the RA (default router, preference, prefixes) is left to
// the kernel. The routes are installed the way the kernel would install
// them: next hop = RA source address, protocol "ra", with the advertised
// lifetime; a zero lifetime removes the route.
package main

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/net/ipv6"
)

const (
	raHeaderLen      = 16 // ICMPv6 header (4) + RA fields (12)
	optRouteInfo     = 24
	allNodesLinkAddr = "ff02::1"
)

// route is one Route Information Option.
type route struct {
	prefix     netip.Prefix
	preference string
	lifetime   uint32 // seconds
}

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s <interface>", os.Args[0])
	}
	ifi, err := net.InterfaceByName(os.Args[1])
	if err != nil {
		log.Fatalf("interface %s: %v", os.Args[1], err)
	}
	raw, err := net.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer raw.Close()
	conn := ipv6.NewPacketConn(raw)
	if err := conn.JoinGroup(ifi, &net.IPAddr{IP: net.ParseIP(allNodesLinkAddr)}); err != nil {
		log.Fatalf("join %s on %s: %v", allNodesLinkAddr, ifi.Name, err)
	}
	// The hop limit and the receiving interface are reported only on request.
	if err := conn.SetControlMessage(ipv6.FlagHopLimit|ipv6.FlagInterface, true); err != nil {
		log.Fatalf("enable control messages: %v", err)
	}
	var filter ipv6.ICMPFilter
	filter.SetAll(true)
	filter.Accept(ipv6.ICMPTypeRouterAdvertisement)
	if err := conn.SetICMPFilter(&filter); err != nil {
		log.Fatalf("set ICMPv6 filter: %v", err)
	}
	log.Printf("rio-shim: kernel lacks RIO support; handling Route Information Options on %s", ifi.Name)

	buf := make([]byte, 65535)
	for {
		n, cm, src, err := conn.ReadFrom(buf)
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		ip, ok := src.(*net.IPAddr)
		if !ok || cm == nil || cm.IfIndex != ifi.Index {
			continue
		}
		from, ok := netip.AddrFromSlice(ip.IP)
		// RFC 4861 Section 6.1.2: an RA must come from a link-local address
		// with hop limit 255, i.e. from a router on this link.
		if !ok || !from.IsLinkLocalUnicast() || cm.HopLimit != 255 {
			continue
		}
		for _, r := range routeInfos(buf[:n]) {
			apply(ifi.Name, from, r)
		}
	}
}

// routeInfos extracts the Route Information Options of a Router
// Advertisement (the ICMPv6 message, starting at its type field).
func routeInfos(msg []byte) []route {
	if len(msg) < raHeaderLen || ipv6.ICMPType(msg[0]) != ipv6.ICMPTypeRouterAdvertisement {
		return nil
	}
	var routes []route
	for opts := msg[raHeaderLen:]; len(opts) >= 2; {
		size := int(opts[1]) * 8
		if size == 0 || size > len(opts) {
			return routes // malformed option: stop, as a receiver must
		}
		if opts[0] == optRouteInfo && size >= 8 {
			bits := int(opts[2])
			var addr [16]byte
			copy(addr[:], opts[8:size])
			// RFC 4191 Section 2.3: the option holds 0, 8 or 16 prefix bytes.
			if bits <= 128 && bits <= (size-8)*8 {
				routes = append(routes, route{
					prefix:     netip.PrefixFrom(netip.AddrFrom16(addr), bits).Masked(),
					preference: preference(opts[3] >> 3 & 0x3),
					lifetime:   binary.BigEndian.Uint32(opts[4:8]),
				})
			}
		}
		opts = opts[size:]
	}
	return routes
}

// apply installs or removes the route of one Route Information Option.
func apply(ifname string, gateway netip.Addr, r route) {
	if r.prefix.Bits() == 0 || r.preference == "" {
		// ::/0 in a RIO would duplicate the default router logic of the
		// kernel; the lab never sends it. A reserved preference means the
		// option must be ignored (RFC 4191 Section 2.3).
		return
	}

	// The kernel keeps one route per (prefix, router), so two routers may
	// advertise the same prefix side by side. iproute2 identifies a route by
	// (prefix, metric), so each router gets its own metric here; otherwise a
	// second router would silently replace the first one's route.
	metric := fmt.Sprint(metricOf(gateway))
	args := []string{"-6", "route"}
	if r.lifetime == 0 {
		args = append(args, "del", r.prefix.String(), "via", gateway.String(), "dev", ifname,
			"proto", "ra", "metric", metric)
	} else {
		args = append(args, "replace", r.prefix.String(), "via", gateway.String(), "dev", ifname,
			"proto", "ra", "metric", metric, "pref", r.preference, "expires", fmt.Sprint(r.lifetime))
	}

	out, err := exec.Command("ip", args...).CombinedOutput()
	// Removing a route that is already gone is not an error worth reporting.
	if err != nil && r.lifetime != 0 {
		log.Printf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

// metricOf maps a router address to a route metric in [1024, 5120). 1024 is
// the metric the kernel uses for RA routes.
func metricOf(gateway netip.Addr) uint32 {
	h := fnv.New32a()
	b := gateway.As16()
	h.Write(b[:])
	return 1024 + h.Sum32()%4096
}

// preference decodes the 2-bit Prf field (RFC 4191 Section 2.1); "" is the
// reserved value.
func preference(prf byte) string {
	switch prf {
	case 0b01:
		return "high"
	case 0b00:
		return "medium"
	case 0b11:
		return "low"
	default:
		return ""
	}
}
