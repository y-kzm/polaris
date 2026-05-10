package store

import "encoding/json"

type Router struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Address          string `json:"address"`
	Interface        string `json:"interface"`
	Status           string `json:"status"`
	RAIntervalMs     int32  `json:"ra_interval_milliseconds"`
	CurrentHopLimit  int32  `json:"current_hop_limit"`
	Managed          bool   `json:"managed"`
	Other            bool   `json:"other"`
	RouterLifetimeS  int32  `json:"router_lifetime_seconds"`
	ReachableTimeMs  int32  `json:"reachable_time_milliseconds"`
	RetransmitTimeMs int32  `json:"retransmit_time_milliseconds"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type Entry struct {
	Type    string `json:"type"`
	Value   string `json:"value"`
	Service string `json:"service,omitempty"`
}

type Rule struct {
	ID        int     `json:"id"`
	Comment   string  `json:"comment"`
	Entries   []Entry `json:"entries"`
	Nexthop   string  `json:"nexthop"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type Group struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Rules     []int    `json:"rules"`
	Members   []string `json:"members"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type Neighbor struct {
	ID        string `json:"id"`
	LLAddr    string `json:"lladdr"`
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	State     string `json:"state"`
	IsRouter  int    `json:"is_router"`
	Source    string `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
