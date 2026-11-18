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
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"

	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s <interface>", os.Args[0])
	}
	ifi, err := net.InterfaceByName(os.Args[1])
	if err != nil {
		log.Fatalf("interface %s: %v", os.Args[1], err)
	}
	conn, _, err := ndp.Listen(ifi, ndp.Unspecified)
	if err != nil {
		log.Fatalf("listen on %s: %v", ifi.Name, err)
	}
	defer conn.Close()
	// The hop limit of a received packet is reported only on request.
	if err := conn.SetControlMessage(ipv6.FlagHopLimit, true); err != nil {
		log.Fatalf("enable hop limit reporting: %v", err)
	}
	log.Printf("rio-shim: kernel lacks RIO support; handling Route Information Options on %s", ifi.Name)

	for {
		msg, cm, from, err := conn.ReadFrom()
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		ra, ok := msg.(*ndp.RouterAdvertisement)
		// RFC 4861 Section 6.1.2: an RA must come from a link-local address
		// with hop limit 255, i.e. from a router on this link.
		if !ok || !from.IsLinkLocalUnicast() || cm == nil || cm.HopLimit != 255 {
			continue
		}
		for _, opt := range ra.Options {
			if ri, ok := opt.(*ndp.RouteInformation); ok {
				apply(ifi.Name, from.WithZone(""), ri)
			}
		}
	}
}

// apply installs or removes the route of one Route Information Option.
func apply(ifname string, gateway netip.Addr, ri *ndp.RouteInformation) {
	prefix := netip.PrefixFrom(ri.Prefix, int(ri.PrefixLength)).Masked()
	if prefix.Bits() == 0 {
		// ::/0 in a RIO would duplicate the default router logic of the
		// kernel; the lab never sends it.
		return
	}

	args := []string{"-6", "route"}
	seconds := int(ri.RouteLifetime.Seconds())
	if seconds == 0 {
		args = append(args, "del", prefix.String(), "via", gateway.String(), "dev", ifname, "proto", "ra")
	} else {
		args = append(args, "replace", prefix.String(), "via", gateway.String(), "dev", ifname,
			"proto", "ra", "metric", "1024", "pref", preference(ri.Preference), "expires", fmt.Sprint(seconds))
	}

	out, err := exec.Command("ip", args...).CombinedOutput()
	// Removing a route that is already gone is not an error worth reporting.
	if err != nil && seconds != 0 {
		log.Printf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func preference(p ndp.Preference) string {
	switch p {
	case ndp.High:
		return "high"
	case ndp.Low:
		return "low"
	default:
		return "medium"
	}
}
