package main

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// oidOf renders bytes as dotted OID sub-identifiers.
func oidOf(b ...byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, ".")
}

var (
	llBytes  = []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	llOID    = oidOf(llBytes...)
	zoneOID  = "0.0.0.3"
	guaBytes = []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff}
)

func TestParseNeighborIndex(t *testing.T) {
	tests := []struct {
		name      string
		index     string
		wantIf    int
		wantIP    string
		wantErr   bool
		wantNotV6 bool
	}{
		{name: "ipv6", index: "3.2.16." + llOID, wantIf: 3, wantIP: "fe80::1"},
		{name: "ipv6z drops the zone", index: "7.4.20." + llOID + "." + zoneOID, wantIf: 7, wantIP: "fe80::1"},
		{name: "ipv4 is not ipv6", index: "3.1.4.192.0.2.1", wantErr: true, wantNotV6: true},
		{name: "ipv6 with 4 bytes", index: "3.2.4.192.0.2.1", wantErr: true},
		{name: "ipv6 with 20 bytes", index: "3.2.20." + llOID + "." + zoneOID, wantErr: true},
		{name: "ipv6z with 16 bytes", index: "3.4.16." + llOID, wantErr: true},
		{name: "length says 16, 15 follow", index: "3.2.16." + oidOf(llBytes[:15]...), wantErr: true},
		{name: "length says 16, 17 follow", index: "3.2.16." + llOID + ".9", wantErr: true},
		{name: "sub-identifier above 255", index: "3.2.16." + strings.Replace(llOID, "254", "510", 1), wantErr: true},
		{name: "negative sub-identifier", index: "3.2.16." + strings.Replace(llOID, "254", "-2", 1), wantErr: true},
		{name: "non-numeric sub-identifier", index: "3.2.16." + strings.Replace(llOID, "254", "x", 1), wantErr: true},
		{name: "ifIndex zero", index: "0.2.16." + llOID, wantErr: true},
		{name: "ifIndex not a number", index: "a.2.16." + llOID, wantErr: true},
		{name: "too short", index: "3.2", wantErr: true},
		{name: "empty", index: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ifIndex, ip, err := parseNeighborIndex(tt.index)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got ifIndex=%d ip=%v", ifIndex, ip)
				}
				if got := errors.Is(err, errNotIPv6); got != tt.wantNotV6 {
					t.Fatalf("errors.Is(err, errNotIPv6) = %v, want %v (err: %v)", got, tt.wantNotV6, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ifIndex != tt.wantIf || ip.String() != tt.wantIP {
				t.Fatalf("got ifIndex=%d ip=%s, want %d %s", ifIndex, ip, tt.wantIf, tt.wantIP)
			}
		})
	}
}

func TestParseInetCidrRouteNextHop(t *testing.T) {
	// ::/0 via the next hop, policy OID 0.0 (2 arcs).
	destDefault := "2.16." + oidOf(make([]byte, 16)...) + ".0.2.0.0"
	tests := []struct {
		name      string
		index     string
		want      string
		wantErr   bool
		wantNotV6 bool
	}{
		{name: "ipv6 next hop", index: destDefault + ".2.16." + llOID, want: "fe80::1"},
		{name: "ipv6z next hop", index: destDefault + ".4.20." + llOID + "." + zoneOID, want: "fe80::1"},
		{name: "policy arc above 255 is skipped", index: "2.16." + oidOf(make([]byte, 16)...) + ".0.2.1000.3.2.16." + llOID, want: "fe80::1"},
		{name: "ipv4 route", index: "1.4.0.0.0.0.0.2.0.0.1.4.192.0.2.1", wantErr: true, wantNotV6: true},
		{name: "next hop byte above 255", index: destDefault + ".2.16." + strings.Replace(llOID, "254", "300", 1), wantErr: true},
		{name: "next hop too short", index: destDefault + ".2.16." + oidOf(llBytes[:10]...), wantErr: true},
		{name: "trailing sub-identifiers", index: destDefault + ".2.16." + llOID + ".1", wantErr: true},
		{name: "dest length beyond index", index: "2.200.1.2.3", wantErr: true},
		{name: "negative dest length", index: "2.-5.1.2.3", wantErr: true},
		{name: "empty", index: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := parseInetCidrRouteNextHop(tt.index)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", ip)
				}
				if got := errors.Is(err, errNotIPv6); got != tt.wantNotV6 {
					t.Fatalf("errors.Is(err, errNotIPv6) = %v, want %v (err: %v)", got, tt.wantNotV6, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ip.String() != tt.want {
				t.Fatalf("got %s, want %s", ip, tt.want)
			}
		})
	}
}

func TestExportableState(t *testing.T) {
	tests := []struct {
		state    int
		wantName string
		wantOK   bool
	}{
		{1, "REACHABLE", true},
		{2, "STALE", true},
		{3, "DELAY", true},
		{4, "PROBE", true},
		{5, "", false}, // INVALID
		{6, "", false}, // UNKNOWN
		{7, "", false}, // INCOMPLETE
		{0, "", false}, // state column missing
		{8, "", false},
		{-1, "", false},
	}
	for _, tt := range tests {
		name, ok := exportableState(tt.state)
		if name != tt.wantName || ok != tt.wantOK {
			t.Errorf("exportableState(%d) = %q, %v; want %q, %v", tt.state, name, ok, tt.wantName, tt.wantOK)
		}
	}
}

func TestExportNeighbors(t *testing.T) {
	mac := []byte{0x02, 0, 0, 0, 0, 0x01}
	ip := func(last byte) net.IP {
		b := append([]byte(nil), llBytes...)
		b[15] = last
		return net.IP(b)
	}
	entries := []neighborEntry{
		{ifIndex: 2, ip: ip(1), mac: mac, state: 1},                 // kept
		{ifIndex: 2, ip: ip(2), mac: mac, state: 2},                 // kept
		{ifIndex: 2, ip: ip(3), mac: nil, state: 1},                 // no MAC
		{ifIndex: 2, ip: ip(4), mac: mac, state: 7},                 // INCOMPLETE
		{ifIndex: 2, ip: ip(5), mac: mac, state: 5},                 // INVALID
		{ifIndex: 2, ip: ip(6), mac: mac, state: 0},                 // no state
		{ifIndex: 3, ip: ip(7), mac: mac, state: 1},                 // other interface
		{ifIndex: 9, ip: ip(8), mac: mac, state: 1},                 // interface without a name
		{ifIndex: 2, ip: net.IP(guaBytes), mac: mac, state: 4},      // kept, known next hop
		{ifIndex: 2, ip: ip(9), mac: []byte{}, state: 3},            // empty MAC
		{ifIndex: 2, ip: ip(10), mac: []byte{0xaa, 0xbb}, state: 3}, // kept
	}
	ifNames := map[int]string{2: "eth1", 3: "eth0"}
	routers := map[string]struct{}{"2001:db8::ff": {}}
	now := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)

	ips := func(ns []neighbor) string {
		var s []string
		for _, n := range ns {
			s = append(s, n.IP)
		}
		return strings.Join(s, " ")
	}

	filtered := exportNeighbors(entries, ifNames, routers, "eth1", now)
	if got, want := ips(filtered), "fe80::1 fe80::2 2001:db8::ff fe80::a"; got != want {
		t.Fatalf("with ifname filter: got %q, want %q", got, want)
	}
	first := filtered[0]
	want := neighbor{IP: "fe80::1", LLAddr: "02:00:00:00:00:01", IfIndex: 2, IfName: "eth1", State: "REACHABLE", UpdatedAt: "2026-11-20T01:02:03Z"}
	if first != want {
		t.Fatalf("got %+v, want %+v", first, want)
	}
	if filtered[2].IsRouter != 1 || filtered[2].State != "PROBE" {
		t.Fatalf("next hop entry: got %+v, want is_router=1 state=PROBE", filtered[2])
	}

	all := exportNeighbors(entries, ifNames, routers, "", now)
	if got, want := ips(all), "fe80::1 fe80::2 fe80::7 fe80::8 2001:db8::ff fe80::a"; got != want {
		t.Fatalf("without filter: got %q, want %q", got, want)
	}

	// With a filter and no interface names nothing may be exported; the
	// caller treats that poll as failed (see collectFrom).
	if got := exportNeighbors(entries, map[int]string{}, routers, "eth1", now); len(got) != 0 {
		t.Fatalf("no names: got %d neighbors, want 0", len(got))
	}
}

func TestParseTargets(t *testing.T) {
	def := Target{Host: "localhost", Port: 161, Community: "public"}
	tests := []struct {
		name    string
		val     string
		want    []Target
		wantErr bool
	}{
		{name: "empty", val: "", want: []Target{def}},
		{name: "null", val: "null", want: []Target{def}},
		{name: "empty array", val: " [] ", want: []Target{def}},
		{name: "json defaults", val: `[{"host":"r1"}]`, want: []Target{{Host: "r1", Port: 161, Community: "public"}}},
		{
			name: "json with interface fields",
			val:  `[{"host":"r1","port":1161,"community":"c","ifname":"eth1","self_ll":"fe80::a","label":"router-a"},{"host":""}]`,
			want: []Target{{Host: "r1", Port: 1161, Community: "c", IfName: "eth1", SelfLL: "fe80::a", Label: "router-a"}},
		},
		{name: "json without hosts", val: `[{"port":161}]`, want: []Target{def}},
		{name: "broken json", val: `[{"host":"r1","community":"secret"`, wantErr: true},
		{name: "json of the wrong shape", val: `["r1"]`, wantErr: true},
		{name: "csv", val: "r1, r2:c2, r3:1161:c3, r4:1161", want: []Target{
			{Host: "r1", Port: 161, Community: "public"},
			{Host: "r2", Port: 161, Community: "c2"},
			{Host: "r3", Port: 1161, Community: "c3"},
			{Host: "r4", Port: 1161, Community: "public"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTargets(tt.val)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("error leaks the community string: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("target %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
