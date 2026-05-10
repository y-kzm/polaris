import { useState, useEffect, useCallback, useRef } from 'react';
import { Network, RefreshCw, Settings, Eye, GitCompare, Save } from 'lucide-react';

import type {
  RouterConfig,
  RouterStatus,
  Rule,
  Group,
  Neighbor,
  FqdnService,
  ServiceEndpoints,
  Entry,
  RAInterfaceConfig,
} from './types';

import { getApiUrl } from './utils/api';
import { generateYaml, generateYamlDiff, hasChanges } from './utils/yaml';
import { createNeighborProvider, createServiceProvider } from './providers';

import RouterGrid from './components/RouterGrid';
import NeighborsTab from './components/NeighborsTab';
import RulesTab from './components/RulesTab';
import GroupsTab from './components/GroupsTab';
import FqdnModal from './components/modals/FqdnModal';
import NeighborModal from './components/modals/NeighborModal';
import RouterConfigModal from './components/modals/RouterConfigModal';
import YamlPreviewModal from './components/modals/YamlPreviewModal';
import YamlDiffModal from './components/modals/YamlDiffModal';
import ConfirmModal from './components/modals/ConfirmModal';
import RAInterfaceModal from './components/modals/RAInterfaceModal';
import NotificationStack, { type NotificationItem } from './components/NotificationStack';

const neighborProvider = createNeighborProvider();
const serviceProvider = createServiceProvider();

const TAB_LABELS: Record<string, string> = {
  neighbors: 'Clients',
  rules: 'RA Policy',
  groups: 'Policy Groups',
};

const RAControllerFrontend = () => {
  // ── Routers ──────────────────────────────────────────────────────────────
  const [routers, setRouters] = useState<RouterConfig[]>([]);
  const [routerStatuses, setRouterStatuses] = useState<Record<string, RouterStatus>>({});
  const [showRouterConfig, setShowRouterConfig] = useState(false);
  const [newRouter, setNewRouter] = useState<Omit<RouterConfig, 'id' | 'status'>>({
    name: '',
    address: '',
    interface: 'eth0',
    ra_interval_milliseconds: 3000,
    current_hop_limit: 64,
    managed: false,
    other: false,
    router_lifetime_seconds: 1800,
    reachable_time_milliseconds: 0,
    retransmit_time_milliseconds: 0,
  });

  // ── RA Policy rules ───────────────────────────────────────────────────────
  const [rules, setRules] = useState<Rule[]>([]);
  const [newRule, setNewRule] = useState<{ comment: string; entries: Entry[]; nexthop: string }>({
    comment: '',
    entries: [],
    nexthop: '',
  });
  const [newRuleEntry, setNewRuleEntry] = useState<Entry>({ type: 'ipv6', value: '' });

  // ── Policy groups ─────────────────────────────────────────────────────────
  const [groups, setGroups] = useState<Group[]>([]);
  const [newGroup, setNewGroup] = useState<{ name: string }>({ name: '' });
  const [expandedGroups, setExpandedGroups] = useState<Record<number, boolean>>({});

  // ── Clients (neighbors) ───────────────────────────────────────────────────
  const [neighbors, setNeighbors] = useState<Neighbor[]>([]);
  const [selectedNeighbors, setSelectedNeighbors] = useState<string[]>([]);
  const [showNeighborModal, setShowNeighborModal] = useState(false);
  const [neighborSources, setNeighborSources] = useState<string>('loading...');
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [refreshCooldown, setRefreshCooldown] = useState(0);
  const cooldownTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // ── FQDN services ─────────────────────────────────────────────────────────
  const [showFqdnModal, setShowFqdnModal] = useState(false);
  const [fqdnServices, setFqdnServices] = useState<FqdnService[]>([]);
  const [serviceEndpoints, setServiceEndpoints] = useState<Record<string, ServiceEndpoints>>({});

  // ── UI ────────────────────────────────────────────────────────────────────
  const [activeTab, setActiveTab] = useState('neighbors');
  const [showYamlPreview, setShowYamlPreview] = useState(false);
  const [showYamlDiff, setShowYamlDiff] = useState(false);
  const [lastCheckedIndex, setLastCheckedIndex] = useState<number | null>(null);
  const [expandedRules, setExpandedRules] = useState<Record<number, boolean>>({});
  const [previousYaml, setPreviousYaml] = useState(() => generateYaml([], []));
  const [showConfirmDeploy, setShowConfirmDeploy] = useState(false);

  // ── RA Interface detail modal ─────────────────────────────────────────────
  const [raInterfaceModal, setRaInterfaceModal] = useState<{
    routerName: string;
    interfaces: RAInterfaceConfig[];
  } | null>(null);

  const openRAInterfaceModal = async (routerId: string, routerName: string) => {
    try {
      const res = await fetch(`${getApiUrl()}/api/routers/${routerId}/interfaces`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const ifaces: RAInterfaceConfig[] = await res.json();
      setRaInterfaceModal({ routerName, interfaces: ifaces });
    } catch (e) {
      notify(`Failed to fetch RA interfaces: ${(e as Error).message}`, 'error');
    }
  };

  // ── Notifications ─────────────────────────────────────────────────────────
  const [notifications, setNotifications] = useState<NotificationItem[]>([]);
  const notifySeq = useRef(0);

  const notify = useCallback((message: string, type: NotificationItem['type'] = 'info') => {
    const id = ++notifySeq.current;
    setNotifications(prev => [...prev, { id, type, message }]);
    setTimeout(() => setNotifications(prev => prev.filter(n => n.id !== id)), 5000);
  }, []);

  const dismissNotification = (id: number) =>
    setNotifications(prev => prev.filter(n => n.id !== id));

  // ── Initialisation ────────────────────────────────────────────────────────
  useEffect(() => {
    loadNeighbors();
    loadRules();
    loadGroups();
    loadRouters();
  }, []);

  useEffect(() => {
    serviceProvider.fetchServices()
      .then(services => setFqdnServices(services))
      .catch(() => setFqdnServices([]));
  }, []);

  // Clean up cooldown timer on unmount.
  useEffect(() => () => {
    if (cooldownTimerRef.current) clearInterval(cooldownTimerRef.current);
  }, []);

  useEffect(() => {
    fetch(`${getApiUrl()}/api/neighbor-sources`)
      .then(r => r.json())
      .then((data: { sources: string[] }) => {
        setNeighborSources(data.sources.length > 0 ? data.sources.join(', ') : 'none');
      })
      .catch(() => setNeighborSources('unknown'));
  }, []);

  // ── Router management ────────────────────────────────────────────────────
  const loadRouters = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/routers`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setRouters(await res.json());
    } catch (e) {
      console.error('Failed to load routers:', e);
    }
  };

  const getRouterStatuses = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/routers/status`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setRouterStatuses(await res.json());
      notify('Router status refreshed', 'success');
    } catch (e) {
      notify(`Failed to poll router status: ${(e as Error).message}`, 'error');
    }
  };

  const addRouter = async () => {
    if (!newRouter.name || !newRouter.address || !newRouter.interface) return;
    try {
      const res = await fetch(`${getApiUrl()}/api/routers`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newRouter),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadRouters();
      setNewRouter({
        name: '', address: '', interface: 'eth0',
        ra_interval_milliseconds: 3000, current_hop_limit: 64,
        managed: false, other: false, router_lifetime_seconds: 1800,
        reachable_time_milliseconds: 0, retransmit_time_milliseconds: 0,
      });
      setShowRouterConfig(false);
      notify(`Router "${newRouter.name}" added`, 'success');
    } catch (e) {
      notify(`Failed to add router: ${(e as Error).message}`, 'error');
    }
  };

  const deleteRouter = async (id: string) => {
    try {
      const res = await fetch(`${getApiUrl()}/api/routers/${id}`, { method: 'DELETE' });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || `HTTP ${res.status}`);
      }
      await loadRouters();
      notify('Router removed', 'success');
    } catch (e) {
      notify(`Failed to remove router: ${(e as Error).message}`, 'error');
    }
  };

  const toggleRouterStatus = async (id: string) => {
    const router = routers.find(r => r.id === id);
    if (!router) return;
    try {
      const newStatus = router.status === 'active' ? 'inactive' : 'active';
      const res = await fetch(`${getApiUrl()}/api/routers/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...router, status: newStatus }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadRouters();
    } catch (e) {
      notify(`Failed to update router status: ${(e as Error).message}`, 'error');
    }
  };

  // ── Clients ───────────────────────────────────────────────────────────────
  const loadNeighbors = async () => {
    try {
      setNeighbors(await neighborProvider.fetchNeighbors());
    } catch (e) {
      console.error('Failed to load ND cache:', e);
    }
  };

  // Triggers an immediate SNMP poll on all collectors, then updates the local list.
  // Disabled for REFRESH_COOLDOWN_SEC seconds after each call to prevent spam.
  const REFRESH_COOLDOWN_SEC = 10;

  const refreshNeighbors = async () => {
    if (isRefreshing || refreshCooldown > 0) return;
    setIsRefreshing(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/neighbors/refresh`, { method: 'POST' });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setNeighbors(await res.json());
      // Start countdown to prevent rapid re-polling.
      setRefreshCooldown(REFRESH_COOLDOWN_SEC);
      if (cooldownTimerRef.current) clearInterval(cooldownTimerRef.current);
      cooldownTimerRef.current = setInterval(() => {
        setRefreshCooldown(prev => {
          if (prev <= 1) {
            clearInterval(cooldownTimerRef.current!);
            cooldownTimerRef.current = null;
            return 0;
          }
          return prev - 1;
        });
      }, 1000);
    } catch (e) {
      notify(`Failed to refresh: ${(e as Error).message}`, 'error');
    } finally {
      setIsRefreshing(false);
    }
  };

  // ── RA Policy rules ───────────────────────────────────────────────────────
  const loadRules = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/rules`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setRules(await res.json());
    } catch (e) {
      console.error('Failed to load policy rules:', e);
    }
  };

  const addRule = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/rules`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ comment: newRule.comment, entries: newRule.entries, nexthop: newRule.nexthop }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadRules();
      setNewRule({ comment: '', entries: [], nexthop: '' });
      setNewRuleEntry({ type: 'ipv6', value: '' });
      notify('Policy rule added', 'success');
    } catch (e) {
      notify(`Failed to add policy rule: ${(e as Error).message}`, 'error');
    }
  };

  const addEntryToNewRule = () => {
    if (!newRuleEntry.value) return;
    setNewRule({ ...newRule, entries: [...newRule.entries, { ...newRuleEntry }] });
    setNewRuleEntry({ type: 'ipv6', value: '' });
  };

  const removeEntryFromNewRule = (index: number) =>
    setNewRule({ ...newRule, entries: newRule.entries.filter((_, i) => i !== index) });

  const toggleRuleExpanded = (ruleId: number) =>
    setExpandedRules(prev => ({ ...prev, [ruleId]: !prev[ruleId] }));

  const fetchServiceEndpoints = async (service: FqdnService) => {
    const serviceName = service.service || service.name;
    if (!serviceName) return null;
    if (serviceEndpoints[serviceName]) return serviceEndpoints[serviceName];
    try {
      const data = await serviceProvider.fetchEndpoints(service);
      if (data) setServiceEndpoints(prev => ({ ...prev, [serviceName]: data }));
      return data;
    } catch {
      return null;
    }
  };

  const addFqdnServiceToRule = async (service: FqdnService) => {
    try {
      const serviceKey = service.service || service.name;
      const displayName = service.name || service.service;
      if (!serviceKey) { notify('Service name is missing', 'error'); return; }

      const endpoints = await fetchServiceEndpoints(service);
      if (!endpoints) { notify('Failed to fetch service endpoints', 'error'); return; }

      const prefixes = endpoints.ipv6_prefixes ?? [];
      if (prefixes.length === 0) { notify('This service has no IPv6 prefixes', 'info'); return; }

      const entries: Entry[] = prefixes.map(prefix => ({ type: 'fqdn', value: prefix, service: serviceKey }));
      setNewRule({ ...newRule, entries: [...newRule.entries, ...entries] });
      setShowFqdnModal(false);
      notify(`Added ${prefixes.length} prefixes from "${displayName}"`, 'success');
    } catch (e) {
      notify(`Failed to fetch prefixes: ${(e as Error).message}`, 'error');
    }
  };

  const deleteRule = async (id: number) => {
    try {
      const res = await fetch(`${getApiUrl()}/api/rules/${id}`, { method: 'DELETE' });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadRules();
      setGroups(groups.map(g => ({ ...g, rules: g.rules.filter(r => r !== id) })));
      notify('Policy rule deleted', 'success');
    } catch (e) {
      notify(`Failed to delete policy rule: ${(e as Error).message}`, 'error');
    }
  };

  // ── Policy groups ─────────────────────────────────────────────────────────
  const loadGroups = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/groups`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setGroups(await res.json());
    } catch (e) {
      console.error('Failed to load policy groups:', e);
    }
  };

  const addGroup = async () => {
    if (!newGroup.name || selectedNeighbors.length === 0) return;
    try {
      const res = await fetch(`${getApiUrl()}/api/groups`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: newGroup.name, members: selectedNeighbors }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadGroups();
      setNewGroup({ name: '' });
      setSelectedNeighbors([]);
      setShowNeighborModal(false);
      notify(`Policy group "${newGroup.name}" created`, 'success');
    } catch (e) {
      notify(`Failed to create policy group: ${(e as Error).message}`, 'error');
    }
  };

  const updateGroupRules = async (id: number, rules: number[]) => {
    try {
      const res = await fetch(`${getApiUrl()}/api/groups/${id}/rules`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rules }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadGroups();
      notify('Rules updated', 'success');
    } catch (e) {
      notify(`Failed to update rules: ${(e as Error).message}`, 'error');
    }
  };

  const deleteGroup = async (id: number) => {
    try {
      const res = await fetch(`${getApiUrl()}/api/groups/${id}`, { method: 'DELETE' });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await loadGroups();
      notify('Policy group deleted', 'success');
    } catch (e) {
      notify(`Failed to delete policy group: ${(e as Error).message}`, 'error');
    }
  };

  const toggleGroupExpanded = (groupId: number) =>
    setExpandedGroups(prev => ({ ...prev, [groupId]: !prev[groupId] }));

  // ── Commit & Deploy ───────────────────────────────────────────────────────
  const applyPolicy = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/policy/apply`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const result = await res.json();

      setPreviousYaml(generateYaml(rules, groups));

      const results: Array<{ rule_id: number; router: string; success: boolean; error?: string }> =
        result.results ?? [];
      const errorCount = results.filter(r => !r.success).length;

      if (result.success) {
        notify(`Policy committed (${results.length} rule(s) applied)`, 'success');
      } else {
        notify(`Policy committed with ${errorCount} error(s) — check router connectivity`, 'error');
      }
    } catch (e) {
      notify(`Commit failed: ${(e as Error).message}`, 'error');
    }
  };

  // ── Derived values ────────────────────────────────────────────────────────
  const currentHasChanges = hasChanges(rules, groups, previousYaml);
  const diffLines = generateYamlDiff(rules, groups, previousYaml);

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div className="min-h-screen bg-gray-100">
      <NotificationStack notifications={notifications} onDismiss={dismissNotification} />

      {/* ── App shell ─────────────────────────────────────────────────── */}
      <div className="max-w-7xl mx-auto p-6 space-y-4">

        {/* ── Header card ────────────────────────────────────────────── */}
        <div className="rounded-lg shadow-lg overflow-hidden">

          {/* Dark title bar */}
          <div className="bg-slate-800 px-6 py-4 flex items-center justify-between">
            <div className="flex items-center gap-3">
              <Network className="w-6 h-6 text-blue-400" />
              <div>
                <h1 className="text-lg font-bold text-white leading-tight">RA Controller</h1>
                <p className="text-xs text-slate-400">IPv6 Router Advertisement Policy Manager</p>
              </div>
            </div>

            {/* Action toolbar */}
            <div className="flex items-center gap-1">
              {/* Secondary: settings */}
              <button
                onClick={() => setShowRouterConfig(true)}
                className="flex items-center gap-1.5 px-3 py-1.5 text-slate-300 hover:text-white hover:bg-slate-700 rounded text-sm transition"
              >
                <Settings className="w-4 h-4" />
                Routers
              </button>

              <div className="w-px h-5 bg-slate-600 mx-1" />

              {/* Secondary: status / view */}
              <button
                onClick={getRouterStatuses}
                className="flex items-center gap-1.5 px-3 py-1.5 text-slate-300 hover:text-white hover:bg-slate-700 rounded text-sm transition"
              >
                <RefreshCw className="w-4 h-4" />
                Poll Status
              </button>
              <button
                onClick={() => setShowYamlPreview(true)}
                className="flex items-center gap-1.5 px-3 py-1.5 text-slate-300 hover:text-white hover:bg-slate-700 rounded text-sm transition"
              >
                <Eye className="w-4 h-4" />
                Preview Config
              </button>
              <button
                onClick={() => setShowYamlDiff(true)}
                className={`flex items-center gap-1.5 px-3 py-1.5 rounded text-sm transition ${
                  currentHasChanges
                    ? 'text-orange-300 hover:bg-slate-700'
                    : 'text-slate-300 hover:text-white hover:bg-slate-700'
                }`}
              >
                <GitCompare className="w-4 h-4" />
                Diff
                {currentHasChanges && (
                  <span className="w-1.5 h-1.5 bg-orange-400 rounded-full" />
                )}
              </button>

              <div className="w-px h-5 bg-slate-600 mx-1" />

              {/* Primary: commit */}
              <button
                onClick={() => setShowConfirmDeploy(true)}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-500 text-sm font-semibold transition"
              >
                <Save className="w-4 h-4" />
                Commit &amp; Deploy
              </button>
            </div>
          </div>

          {/* Router grid */}
          <div className="bg-white p-6">
            <RouterGrid
              routers={routers}
              routerStatuses={routerStatuses}
              onToggleStatus={toggleRouterStatus}
              onInterfaceClick={openRAInterfaceModal}
            />
          </div>
        </div>

        {/* ── Tab content card ───────────────────────────────────────── */}
        <div className="bg-white rounded-lg shadow-lg">
          {/* Tab bar */}
          <div className="border-b">
            <div className="flex">
              {(['neighbors', 'rules', 'groups'] as const).map(tab => (
                <button
                  key={tab}
                  onClick={() => setActiveTab(tab)}
                  className={`px-6 py-4 text-sm font-medium transition ${
                    activeTab === tab
                      ? 'text-blue-600 border-b-2 border-blue-600'
                      : 'text-gray-500 hover:text-gray-800'
                  }`}
                >
                  {TAB_LABELS[tab]}
                </button>
              ))}
            </div>
          </div>

          <div className="p-6">
            {activeTab === 'neighbors' && (
              <NeighborsTab
                neighbors={neighbors}
                groups={groups}
                sourceDescription={neighborSources}
                onRefresh={refreshNeighbors}
                isRefreshing={isRefreshing}
                refreshCooldown={refreshCooldown}
              />
            )}
            {activeTab === 'rules' && (
              <RulesTab
                rules={rules}
                routers={routers}
                groups={groups}
                newRule={newRule}
                newRuleEntry={newRuleEntry}
                expandedRules={expandedRules}
                onNewRuleChange={setNewRule}
                onNewRuleEntryChange={setNewRuleEntry}
                onAddRule={addRule}
                onAddEntry={addEntryToNewRule}
                onRemoveEntry={removeEntryFromNewRule}
                onDeleteRule={deleteRule}
                onToggleExpanded={toggleRuleExpanded}
                onOpenFqdnModal={() => setShowFqdnModal(true)}
              />
            )}
            {activeTab === 'groups' && (
              <GroupsTab
                groups={groups}
                rules={rules}
                routers={routers}
                neighbors={neighbors}
                newGroup={newGroup}
                selectedNeighbors={selectedNeighbors}
                expandedGroups={expandedGroups}
                onNewGroupChange={setNewGroup}
                onSelectedNeighborsChange={setSelectedNeighbors}
                onAddGroup={addGroup}
                onDeleteGroup={deleteGroup}
                onUpdateGroupRules={updateGroupRules}
                onToggleExpanded={toggleGroupExpanded}
                onOpenNeighborModal={() => setShowNeighborModal(true)}
              />
            )}
          </div>
        </div>
      </div>

      {/* ── Modals ──────────────────────────────────────────────────────── */}
      {showConfirmDeploy && (
        <ConfirmModal
          title="Commit & Deploy Policy"
          message="This will push the current configuration to all active routers via gRPC. Continue?"
          confirmLabel="Commit & Deploy"
          onConfirm={() => { setShowConfirmDeploy(false); applyPolicy(); }}
          onCancel={() => setShowConfirmDeploy(false)}
        />
      )}
      {showFqdnModal && (
        <FqdnModal
          fqdnServices={fqdnServices}
          serviceEndpoints={serviceEndpoints}
          onSelectService={addFqdnServiceToRule}
          onFetchEndpoints={fetchServiceEndpoints}
          onClose={() => setShowFqdnModal(false)}
        />
      )}
      {showNeighborModal && (
        <NeighborModal
          neighbors={neighbors}
          selectedNeighbors={selectedNeighbors}
          lastCheckedIndex={lastCheckedIndex}
          sourceDescription={neighborSources}
          onSelectionChange={setSelectedNeighbors}
          onLastCheckedIndexChange={setLastCheckedIndex}
          onClose={() => setShowNeighborModal(false)}
        />
      )}
      {showRouterConfig && (
        <RouterConfigModal
          routers={routers}
          newRouter={newRouter}
          onNewRouterChange={setNewRouter}
          onAddRouter={addRouter}
          onDeleteRouter={deleteRouter}
          onClose={() => setShowRouterConfig(false)}
        />
      )}
      {showYamlPreview && (
        <YamlPreviewModal
          rules={rules}
          groups={groups}
          routers={routers}
          onClose={() => setShowYamlPreview(false)}
        />
      )}
      {showYamlDiff && (
        <YamlDiffModal
          diffLines={diffLines}
          hasChanges={currentHasChanges}
          onClose={() => setShowYamlDiff(false)}
        />
      )}
      {raInterfaceModal && (
        <RAInterfaceModal
          routerName={raInterfaceModal.routerName}
          interfaces={raInterfaceModal.interfaces}
          onClose={() => setRaInterfaceModal(null)}
        />
      )}
    </div>
  );
};

export default RAControllerFrontend;
