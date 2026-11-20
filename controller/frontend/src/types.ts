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
  source_address?: string;
  description?: string;
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
  /** Link-local source address of the policy RAs; empty = same as the baseline RA. */
  policy_source_address: string;
  ra_interval_milliseconds: number;
  current_hop_limit: number;
  managed: boolean;
  other: boolean;
  router_lifetime_seconds: number;
  reachable_time_milliseconds: number;
  retransmit_time_milliseconds: number;
  /**
   * active: takes part in the policy. inactive: excluded from the policy; its
   * policy RAs are withdrawn at the next apply.
   */
  status: RouterAdminStatus;
  created_at?: string;
  updated_at?: string;
}

export type RouterAdminStatus = 'active' | 'inactive';

/** What POST /api/routers and PUT /api/routers/{id} accept. */
export type RouterInput = Omit<RouterConfig, 'id' | 'created_at' | 'updated_at'>;

/** Answer of DELETE /api/routers/{id}. */
export interface RouterDeleteResult {
  withdrawn_hosts: number;
  warning?: string;
}

/**
 * A rule entry. `ipv6`: value is a literal prefix (it may carry a `service`
 * label written by earlier versions). `service`: value is the name of a
 * service; the backend resolves its prefixes at apply time.
 */
export interface Entry {
  type: 'ipv6' | 'service';
  value: string;
  service?: string;
}

/** What POST /api/rules and PUT /api/rules/{id} accept. */
export interface RuleInput {
  comment: string;
  entries: Entry[];
  nexthop: string;
}

/** What POST /api/groups and PUT /api/groups/{id} accept (full replace). */
export interface GroupInput {
  name: string;
  members: string[];
  rules: number[];
}

export interface Rule {
  id: number;
  comment: string;
  entries: Entry[];
  nexthop: string;
  created_at?: string;
  updated_at?: string;
}

export interface Group {
  id: number;
  name: string;
  rules: number[];
  members: string[];
  created_at?: string;
  updated_at?: string;
}

export interface Neighbor {
  id: string;
  lladdr: string;
  ifname: string;
  state: string;
  is_router: number;
  ifindex?: number;
  source?: string;
  updated_at: string;
}

export interface FqdnService {
  service?: string;
  name: string;
  description?: string;
}

export interface ServiceEndpoints {
  fqdns?: string[];
  ipv6_prefixes?: string[];
  ipv4_prefixes?: string[];
}

/** One control request message of Algorithm 1, as it is sent to the hosts. */
export interface PlannedMessage {
  source_address: string;
  preference: 'high' | 'medium' | 'low';
  router_lifetime_seconds: number;
  routes: string[] | null;
  clients: string[] | null;
}

/** Per-router part of a plan or of an apply result. */
export interface RouterResult {
  router: string;
  address: string;
  status: RouterAdminStatus;
  reachable: boolean;
  success: boolean;
  error?: string;
  note?: string;
  messages?: PlannedMessage[];
  added: number;
  updated: number;
  deleted: number;
  unchanged: number;
  /** Number of hosts that get (or got) a withdrawal RA. */
  withdrawn: number;
}

/** Answer of GET /api/policy/plan. */
export interface PolicyPlan {
  pending: boolean;
  applied_at: string;
  warnings: string[] | null;
  routers: RouterResult[] | null;
}

/** Answer of POST /api/policy/apply (status 200, or 207 if a router failed). */
export interface ApplyResult {
  results: RouterResult[] | null;
  warnings: string[] | null;
  success: boolean;
  dry_run: boolean;
  applied_at?: string;
}

export interface NeighborSourceStatus {
  name: string;
  url: string;
  ok: boolean;
  error: string;
  last_success: string;
  count: number;
}

export interface EndpointSourceStatus {
  url: string;
  ok: boolean;
  error: string;
  last_success: string;
}

/** Answer of GET /api/sources. */
export interface SourcesStatus {
  neighbors: NeighborSourceStatus[];
  endpoints: EndpointSourceStatus[];
}

/** What the service picker and the service entries need; provided by App. */
export interface ServiceCatalog {
  services: FqdnService[];
  /** Why the list of services could not be loaded, if it could not. */
  error: string | null;
  sources: SourcesStatus | null;
  reload: () => void;
  /** Rejects with the backend's message (404 unknown service, 502 no source answered). */
  fetchEndpoints: (serviceName: string) => Promise<ServiceEndpoints>;
}

/** Progress of one POST /api/policy/apply. */
export type ApplyState =
  | { status: 'running' }
  | { status: 'done'; result: ApplyResult }
  | { status: 'error'; message: string };
