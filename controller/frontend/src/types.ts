// ============================================================
// Shared TypeScript interfaces for RA Controller Frontend
// ============================================================

export interface RouterIface {
  name: string;
  id: string;
  state: string;
  tx_solicited_ra: number;
  tx_unsolicited_ra: number;
}

export interface RAPrefix {
  prefix: string;
  on_link: boolean;
  autonomous: boolean;
  valid_lifetime_seconds?: { value: number } | null;
  preferred_lifetime_seconds?: { value: number } | null;
}

export interface RARoute {
  prefix: string;
  lifetime_seconds: number;
  preference: string;
}

export interface RAInterfaceConfig {
  id: number;
  name: string;
  ra_interval_milliseconds: number;
  current_hop_limit: number;
  managed: boolean;
  other: boolean;
  preference: string;
  router_lifetime_seconds: number;
  reachable_time_milliseconds: number;
  retransmit_time_milliseconds: number;
  disable_rs_reply: boolean;
  prefixes: RAPrefix[];
  routes: RARoute[];
  clients: string[];
}

export interface RouterStatus {
  reachable: boolean;
  error?: string;
  interfaces: RouterIface[];
  name?: string;
  address?: string;
}

export interface RouterConfig {
  id: string;
  name: string;
  address: string;
  interface: string;
  ra_interval_milliseconds: number;
  current_hop_limit: number;
  managed: boolean;
  other: boolean;
  router_lifetime_seconds: number;
  reachable_time_milliseconds: number;
  retransmit_time_milliseconds: number;
  status?: string;
}

export interface Entry {
  type: 'ipv6' | 'fqdn';
  value: string;
  service?: string;
}

export interface Rule {
  id: number;
  comment: string;
  entries: Entry[];
  nexthop: string;
}

export interface Group {
  id: number;
  name: string;
  rules: number[];
  members: string[];
}

export interface Neighbor {
  id: string;
  lladdr: string;
  ifname: string;
  state: string;
  is_router: number;
  source?: string;
  updated_at: string;
}

export interface FqdnService {
  service?: string;
  name?: string;
  instance?: string;
  version?: string;
  description?: string;
}

export interface ServiceEndpoints {
  fqdns?: string[];
  ipv6_prefixes?: string[];
  ipv4_prefixes?: string[];
}

export interface DiffLine {
  type: 'added' | 'removed' | 'unchanged';
  content: string;
  lineNum: number;
}
