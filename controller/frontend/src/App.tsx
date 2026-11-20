import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Network, RefreshCw, Settings, ListChecks, Save } from 'lucide-react';

import type {
  RouterConfig,
  RouterStatus,
  RouterInput,
  RouterDeleteResult,
  Rule,
  RuleInput,
  Group,
  GroupInput,
  Neighbor,
  FqdnService,
  RAInterfaceConfig,
  SourcesStatus,
  ServiceCatalog,
  ApplyResult,
  ApplyState,
} from './types';

import { api, errorMessage } from './utils/api';
import { createNeighborProvider, createServiceProvider } from './providers';
import { usePolicyPlan } from './hooks/usePolicyPlan';

import RouterGrid from './components/RouterGrid';
import NeighborsTab from './components/NeighborsTab';
import RulesTab from './components/RulesTab';
import GroupsTab from './components/GroupsTab';
import RouterConfigModal from './components/modals/RouterConfigModal';
import PlanModal from './components/modals/PlanModal';
import ApplyConfirmModal from './components/modals/ApplyConfirmModal';
import ApplyResultModal from './components/modals/ApplyResultModal';
import RAInterfaceModal from './components/modals/RAInterfaceModal';
import NotificationStack, { type NotificationItem } from './components/NotificationStack';

const neighborProvider = createNeighborProvider();
const serviceProvider = createServiceProvider();

const TAB_LABELS: Record<string, string> = {
  neighbors: 'Clients',
  rules: 'RA Policy',
  groups: 'Policy Groups',
};

// Seconds the Refresh button stays disabled after a poll of the sources.
const REFRESH_COOLDOWN_SEC = 10;

const RAControllerFrontend = () => {
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

  // ── State (everything below is what the backend answered) ────────────────
  const [routers, setRouters] = useState<RouterConfig[]>([]);
  const [routerStatuses, setRouterStatuses] = useState<Record<string, RouterStatus>>({});
  const [rules, setRules] = useState<Rule[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [neighbors, setNeighbors] = useState<Neighbor[]>([]);
  const [sources, setSources] = useState<SourcesStatus | null>(null);
  const [sourcesError, setSourcesError] = useState<string | null>(null);
  const [fqdnServices, setFqdnServices] = useState<FqdnService[]>([]);
  const [servicesError, setServicesError] = useState<string | null>(null);

  // The plan is computed by the backend; `pending` drives the "unapplied
  // changes" indicator.
  const { plan, error: planError, loading: planLoading, refresh: refreshPlan, refreshNow: refreshPlanNow } =
    usePolicyPlan();

  // ── UI ────────────────────────────────────────────────────────────────────
  const [activeTab, setActiveTab] = useState('neighbors');
  const [showRouterConfig, setShowRouterConfig] = useState(false);
  const [showPlan, setShowPlan] = useState(false);
  const [showConfirmDeploy, setShowConfirmDeploy] = useState(false);
  const [applyState, setApplyState] = useState<ApplyState | null>(null);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [refreshCooldown, setRefreshCooldown] = useState(0);
  const cooldownTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const [raInterfaceModal, setRaInterfaceModal] = useState<{
    routerName: string;
    interfaces: RAInterfaceConfig[];
  } | null>(null);

  // ── Loading ───────────────────────────────────────────────────────────────
  const loadRouters = useCallback(async () => {
    try {
      setRouters((await api<RouterConfig[] | null>('/api/routers')) ?? []);
    } catch (e) {
      notify(`Failed to load routers: ${errorMessage(e)}`, 'error');
    }
  }, [notify]);

  const loadRules = useCallback(async () => {
    try {
      setRules((await api<Rule[] | null>('/api/rules')) ?? []);
    } catch (e) {
      notify(`Failed to load policy rules: ${errorMessage(e)}`, 'error');
    }
  }, [notify]);

  const loadGroups = useCallback(async () => {
    try {
      setGroups((await api<Group[] | null>('/api/groups')) ?? []);
    } catch (e) {
      notify(`Failed to load policy groups: ${errorMessage(e)}`, 'error');
    }
  }, [notify]);

  const loadSources = useCallback(async () => {
    try {
      setSources(await api<SourcesStatus>('/api/sources'));
      setSourcesError(null);
    } catch (e) {
      setSourcesError(errorMessage(e));
    }
  }, []);

  const loadNeighbors = useCallback(async () => {
    try {
      setNeighbors(await neighborProvider.fetchNeighbors());
    } catch (e) {
      notify(`Failed to load clients: ${errorMessage(e)}`, 'error');
    }
    await loadSources();
  }, [notify, loadSources]);

  const loadServices = useCallback(async () => {
    try {
      setFqdnServices(await serviceProvider.fetchServices());
      setServicesError(null);
    } catch (e) {
      setServicesError(errorMessage(e));
    }
    // Listing the services is what tells the backend whether its endpoint
    // sources answer.
    await loadSources();
  }, [loadSources]);

  useEffect(() => {
    void loadRouters();
    void loadRules();
    void loadGroups();
    void loadNeighbors();
    void loadServices();
  }, [loadRouters, loadRules, loadGroups, loadNeighbors, loadServices]);

  // Clean up cooldown timer on unmount.
  useEffect(() => () => {
    if (cooldownTimerRef.current) clearInterval(cooldownTimerRef.current);
  }, []);

  // ── Router management ────────────────────────────────────────────────────
  const pollRouterStatuses = useCallback(async (quiet: boolean) => {
    try {
      setRouterStatuses((await api<Record<string, RouterStatus> | null>('/api/routers/status')) ?? {});
      if (!quiet) notify('Router status refreshed', 'success');
    } catch (e) {
      notify(`Failed to poll router status: ${errorMessage(e)}`, 'error');
    }
  }, [notify]);

  const openRAInterfaceModal = async (routerId: string, routerName: string) => {
    try {
      const interfaces = (await api<RAInterfaceConfig[] | null>(`/api/routers/${routerId}/interfaces`)) ?? [];
      setRaInterfaceModal({ routerName, interfaces });
    } catch (e) {
      notify(`Failed to fetch RA interfaces: ${errorMessage(e)}`, 'error');
    }
  };

  // Creates or replaces a router. Rejects with the backend's message, so the
  // form that called it stays open.
  const saveRouter = async (input: RouterInput, id?: string) => {
    if (id) {
      await api<RouterConfig>(`/api/routers/${id}`, { method: 'PUT', body: input });
    } else {
      await api<RouterConfig>('/api/routers', { method: 'POST', body: input });
    }
    await loadRouters();
    refreshPlan();
    notify(`Router "${input.name.trim()}" ${id ? 'saved' : 'added'}`, 'success');
  };

  const deleteRouter = async (id: string, force: boolean) => {
    const result = await api<RouterDeleteResult>(`/api/routers/${id}${force ? '?force=1' : ''}`, { method: 'DELETE' });
    setRouterStatuses(prev => {
      const next = { ...prev };
      delete next[id];
      return next;
    });
    await loadRouters();
    refreshPlan();
    return result;
  };

  const toggleRouterStatus = async (id: string) => {
    const router = routers.find(r => r.id === id);
    if (!router) return;
    const status = router.status === 'active' ? 'inactive' : 'active';
    try {
      await api<RouterConfig>(`/api/routers/${id}`, { method: 'PUT', body: { ...router, status } });
      await loadRouters();
      refreshPlan();
      notify(
        status === 'inactive'
          ? `${router.name} is now inactive: it is excluded from the policy, and its policy RAs are withdrawn at the next apply`
          : `${router.name} is now active: it takes part in the policy from the next apply`,
        'info',
      );
    } catch (e) {
      notify(`Failed to update router status: ${errorMessage(e)}`, 'error');
    }
  };

  // ── Clients ───────────────────────────────────────────────────────────────
  // Asks every neighbor source to refresh now, then updates the local list.
  // Disabled for REFRESH_COOLDOWN_SEC seconds after each call to prevent spam.
  const refreshNeighbors = async () => {
    if (isRefreshing || refreshCooldown > 0) return;
    setIsRefreshing(true);
    try {
      setNeighbors(await neighborProvider.refreshNeighbors());
      setRefreshCooldown(REFRESH_COOLDOWN_SEC);
      if (cooldownTimerRef.current) clearInterval(cooldownTimerRef.current);
      cooldownTimerRef.current = setInterval(() => {
        setRefreshCooldown(prev => {
          if (prev <= 1) {
            if (cooldownTimerRef.current) clearInterval(cooldownTimerRef.current);
            cooldownTimerRef.current = null;
            return 0;
          }
          return prev - 1;
        });
      }, 1000);
    } catch (e) {
      notify(`Failed to refresh: ${errorMessage(e)}`, 'error');
    } finally {
      setIsRefreshing(false);
      void loadSources();
    }
  };

  // ── RA Policy rules ───────────────────────────────────────────────────────
  // The backend's answer holds the canonical values; it becomes the state.
  const saveRule = async (input: RuleInput, id?: number) => {
    if (id !== undefined) {
      const saved = await api<Rule>(`/api/rules/${id}`, { method: 'PUT', body: input });
      setRules(prev => prev.map(r => (r.id === id ? saved : r)));
    } else {
      const saved = await api<Rule>('/api/rules', { method: 'POST', body: input });
      setRules(prev => [...prev, saved]);
    }
    refreshPlan();
    notify(id !== undefined ? 'Policy rule saved' : 'Policy rule added', 'success');
  };

  const deleteRule = async (id: number) => {
    try {
      await api<void>(`/api/rules/${id}`, { method: 'DELETE' });
      // The backend removed the rule from every group as well.
      await Promise.all([loadRules(), loadGroups()]);
      refreshPlan();
      notify('Policy rule deleted', 'success');
    } catch (e) {
      notify(`Failed to delete policy rule: ${errorMessage(e)}`, 'error');
    }
  };

  // ── Policy groups ─────────────────────────────────────────────────────────
  const saveGroup = async (input: { name: string; members: string[] }, id?: number) => {
    if (id !== undefined) {
      // PUT replaces the whole group, so its rules are sent along unchanged.
      const body: GroupInput = { ...input, rules: groups.find(g => g.id === id)?.rules ?? [] };
      const saved = await api<Group>(`/api/groups/${id}`, { method: 'PUT', body });
      setGroups(prev => prev.map(g => (g.id === id ? saved : g)));
    } else {
      const body: GroupInput = { ...input, rules: [] };
      const saved = await api<Group>('/api/groups', { method: 'POST', body });
      setGroups(prev => [...prev, saved]);
    }
    refreshPlan();
    notify(`Policy group "${input.name.trim()}" ${id !== undefined ? 'saved' : 'created'}`, 'success');
  };

  const updateGroupRules = async (id: number, ruleIds: number[]) => {
    const saved = await api<Group>(`/api/groups/${id}/rules`, { method: 'PUT', body: { rules: ruleIds } });
    setGroups(prev => prev.map(g => (g.id === id ? saved : g)));
    refreshPlan();
    notify('Rules updated', 'success');
  };

  const deleteGroup = async (id: number) => {
    try {
      await api<void>(`/api/groups/${id}`, { method: 'DELETE' });
      await loadGroups();
      refreshPlan();
      notify('Policy group deleted', 'success');
    } catch (e) {
      notify(`Failed to delete policy group: ${errorMessage(e)}`, 'error');
    }
  };

  // ── Services ──────────────────────────────────────────────────────────────
  const fetchEndpoints = useCallback(async (serviceName: string) => {
    try {
      return await serviceProvider.fetchEndpoints(serviceName);
    } finally {
      void loadSources();
    }
  }, [loadSources]);

  const reloadServices = useCallback(() => { void loadServices(); }, [loadServices]);

  const catalog: ServiceCatalog = useMemo(() => ({
    services: fqdnServices,
    error: servicesError,
    sources,
    reload: reloadServices,
    fetchEndpoints,
  }), [fqdnServices, servicesError, sources, reloadServices, fetchEndpoints]);

  // ── Plan, Commit & Deploy ─────────────────────────────────────────────────
  // Opening either view asks for a new plan: the routers' state may have
  // changed since the last edit.
  const openPlan = () => {
    setShowPlan(true);
    void refreshPlanNow();
  };

  const openConfirmDeploy = () => {
    setShowConfirmDeploy(true);
    void refreshPlanNow();
  };

  const applyPolicy = async () => {
    setShowConfirmDeploy(false);
    setApplyState({ status: 'running' });
    try {
      // 200: applied everywhere. 207: some router failed; the body says which.
      const result = await api<ApplyResult>('/api/policy/apply', { method: 'POST' });
      setApplyState({ status: 'done', result });
    } catch (e) {
      setApplyState({ status: 'error', message: errorMessage(e) });
    } finally {
      void pollRouterStatuses(true);
      void refreshPlanNow();
    }
  };

  // ── Derived values ────────────────────────────────────────────────────────
  const pending = plan?.pending ?? false;
  const neighborSourceNames = sources
    ? (sources.neighbors.map(s => s.name || s.url).join(', ') || 'none')
    : sourcesError ? 'unknown' : 'loading...';

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
                onClick={() => pollRouterStatuses(false)}
                className="flex items-center gap-1.5 px-3 py-1.5 text-slate-300 hover:text-white hover:bg-slate-700 rounded text-sm transition"
              >
                <RefreshCw className="w-4 h-4" />
                Poll Status
              </button>
              <button
                onClick={openPlan}
                title={
                  planError ? `The plan could not be computed: ${planError}`
                  : pending ? 'There are unapplied changes'
                  : 'What applying the current draft would do'
                }
                className={`flex items-center gap-1.5 px-3 py-1.5 rounded text-sm transition ${
                  pending
                    ? 'text-orange-300 hover:bg-slate-700'
                    : 'text-slate-300 hover:text-white hover:bg-slate-700'
                }`}
              >
                <ListChecks className="w-4 h-4" />
                Plan
                {pending && <span className="w-1.5 h-1.5 bg-orange-400 rounded-full" />}
                {planError && <span className="w-1.5 h-1.5 bg-red-400 rounded-full" />}
              </button>

              <div className="w-px h-5 bg-slate-600 mx-1" />

              {/* Primary: commit */}
              <button
                onClick={openConfirmDeploy}
                disabled={applyState?.status === 'running'}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-500 disabled:bg-slate-600 text-sm font-semibold transition"
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
                sourceDescription={neighborSourceNames}
                sources={sources}
                sourcesError={sourcesError}
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
                catalog={catalog}
                onSaveRule={saveRule}
                onDeleteRule={deleteRule}
              />
            )}
            {activeTab === 'groups' && (
              <GroupsTab
                groups={groups}
                rules={rules}
                routers={routers}
                neighbors={neighbors}
                sources={sources}
                sourceDescription={neighborSourceNames}
                onSaveGroup={saveGroup}
                onDeleteGroup={deleteGroup}
                onUpdateGroupRules={updateGroupRules}
              />
            )}
          </div>
        </div>
      </div>

      {/* ── Modals ──────────────────────────────────────────────────────── */}
      {showPlan && (
        <PlanModal
          plan={plan}
          error={planError}
          loading={planLoading}
          onRefresh={() => { void refreshPlanNow(); }}
          onClose={() => setShowPlan(false)}
        />
      )}
      {showConfirmDeploy && (
        <ApplyConfirmModal
          plan={plan}
          error={planError}
          loading={planLoading}
          onConfirm={applyPolicy}
          onCancel={() => setShowConfirmDeploy(false)}
        />
      )}
      {applyState && (
        <ApplyResultModal state={applyState} onClose={() => setApplyState(null)} />
      )}
      {showRouterConfig && (
        <RouterConfigModal
          routers={routers}
          onSaveRouter={saveRouter}
          onDeleteRouter={deleteRouter}
          onClose={() => setShowRouterConfig(false)}
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
