package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Target represents a single SNMP-polled router.
// JSON tags match the YAML keys in param.yaml (snmp_targets list).
type Target struct {
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
	Community string `json:"community"`
	// IfName restricts collection to the named site-facing interface.
	IfName string `json:"ifname,omitempty"`
	// SelfLL is the router's own link-local on the site-facing interface.
	// Neighbors matching any target's SelfLL are classified as is_router=1.
	SelfLL string `json:"self_ll,omitempty"`
	// Label is the human-readable name written into the neighbor source field.
	// Falls back to Host when empty.
	Label string `json:"label,omitempty"`
}

// key identifies a target in the per-target cache.
func (t Target) key() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(int(t.Port)))
}

// source is the value written into the neighbor source field.
func (t Target) source() string {
	if t.Label != "" {
		return t.Label
	}
	return t.Host
}

// parseTargets parses the SNMP_TARGETS environment variable.
//
// Two formats are accepted:
//
//   - JSON array (primary, set by server.sh from param.yaml):
//     [{"host":"r1","port":161,"community":"public","ifname":"eth1",
//     "self_ll":"fe80::1","label":"router-a"}, ...]
//     Only host is required.
//
//   - Legacy CSV (fallback, for running start.sh directly):
//     "host", "host:community", or "host:port:community" — comma-separated.
//     Note: IPv6 addresses in this format must be specified as hostnames.
//
// A value that starts with "[" but is not valid JSON is an error: falling
// back to the CSV format would turn fragments of it (possibly community
// strings) into host names.
func parseTargets(val string) ([]Target, error) {
	val = strings.TrimSpace(val)
	if val == "" || val == "null" || val == "[]" {
		return defaultTargets(), nil
	}

	// Primary: JSON array produced by server.sh via module_config_json.
	if strings.HasPrefix(val, "[") {
		var targets []Target
		if err := json.Unmarshal([]byte(val), &targets); err != nil {
			return nil, fmt.Errorf("SNMP_TARGETS is not a valid JSON array of targets: %w", err)
		}
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
			return out, nil
		}
		return defaultTargets(), nil
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
		return defaultTargets(), nil
	}
	return out, nil
}

func defaultTargets() []Target {
	return []Target{{Host: "localhost", Port: 161, Community: "public"}}
}
