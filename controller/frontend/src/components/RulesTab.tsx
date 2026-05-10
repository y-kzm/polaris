import { useState } from 'react';
import { Plus, Trash2, ChevronDown, ChevronRight, X, Globe } from 'lucide-react';
import type { Rule, RouterConfig, Entry, Group } from '../types';

interface RulesTabProps {
  rules: Rule[];
  routers: RouterConfig[];
  groups: Group[];
  newRule: { comment: string; entries: Entry[]; nexthop: string };
  newRuleEntry: Entry;
  expandedRules: Record<number, boolean>;
  onNewRuleChange: (rule: { comment: string; entries: Entry[]; nexthop: string }) => void;
  onNewRuleEntryChange: (entry: Entry) => void;
  onAddRule: () => void;
  onAddEntry: () => void;
  onRemoveEntry: (index: number) => void;
  onDeleteRule: (id: number) => void;
  onToggleExpanded: (ruleId: number) => void;
  onOpenFqdnModal: () => void;
}

export default function RulesTab({
  rules,
  routers,
  groups,
  newRule,
  newRuleEntry,
  expandedRules,
  onNewRuleChange,
  onNewRuleEntryChange,
  onAddRule,
  onAddEntry,
  onRemoveEntry,
  onDeleteRule,
  onToggleExpanded,
  onOpenFqdnModal,
}: RulesTabProps) {
  const [showAddForm, setShowAddForm] = useState(false);

  // The Nexthop field can hold either router.id (UUID) or router.address.
  const findRouter = (nexthop: string) =>
    routers.find(r => r.id === nexthop || r.address === nexthop);

  const routerName = (nexthop: string) => {
    const r = findRouter(nexthop);
    return r ? r.name : nexthop;
  };

  const usedByGroups = (ruleId: number) =>
    groups.filter(g => g.rules.includes(ruleId));

  const handleAddRule = () => {
    onAddRule();
    setShowAddForm(false);
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-base font-semibold text-gray-800">RA Policy Rules</h2>
        {!showAddForm && (
          <button
            onClick={() => setShowAddForm(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm transition"
          >
            <Plus className="w-3.5 h-3.5" />
            Add Policy Rule
          </button>
        )}
      </div>

      {/* Rule list */}
      <div className="space-y-2 mb-4">
        {rules.length === 0 && !showAddForm && (
          <p className="text-sm text-gray-400 py-6 text-center">
            No policy rules defined yet.
          </p>
        )}

        {rules.map(rule => {
          const isDefault = rule.entries.some(e => e.value === '::/0');
          const title = rule.comment || `Rule #${rule.id}`;
          const router = findRouter(rule.nexthop);
          const usingGroups = usedByGroups(rule.id);
          const isExpanded = expandedRules[rule.id];

          // Inline prefix preview shown when collapsed
          const previewPrefixes = rule.entries.slice(0, 2).map(e => e.value);
          const overflowCount = rule.entries.length - 2;

          return (
            <div key={rule.id} className="border rounded-lg p-4">
              <div className="flex justify-between items-start gap-3">
                <div className="flex-1 min-w-0">

                  {/* Title row */}
                  <div className="flex items-center gap-2 mb-2 flex-wrap">
                    <span className="font-semibold text-gray-800 text-sm">{title}</span>
                    {rule.comment && (
                      <span className="text-xs text-gray-400 font-mono">#{rule.id}</span>
                    )}
                    <span className={`px-1.5 py-0.5 text-xs rounded font-medium ${
                      isDefault ? 'bg-purple-100 text-purple-700' : 'bg-blue-100 text-blue-700'
                    }`}>
                      {isDefault ? 'Default Route' : 'Explicit'}
                    </span>
                    {usingGroups.length > 0 && (
                      <span className="px-1.5 py-0.5 text-xs rounded font-medium bg-gray-100 text-gray-500">
                        {usingGroups.length} group{usingGroups.length > 1 ? 's' : ''}
                      </span>
                    )}
                  </div>

                  {/* Prefix list — collapsible with inline preview */}
                  <div className="mb-1.5">
                    <button
                      className="flex items-center gap-1 text-blue-600 hover:text-blue-800 text-xs font-medium"
                      onClick={() => onToggleExpanded(rule.id)}
                    >
                      {isExpanded
                        ? <ChevronDown className="w-3.5 h-3.5" />
                        : <ChevronRight className="w-3.5 h-3.5" />}
                      Prefix List ({rule.entries.length})
                    </button>

                    {/* Collapsed preview */}
                    {!isExpanded && rule.entries.length > 0 && (
                      <div className="mt-0.5 ml-5 flex flex-wrap gap-1">
                        {previewPrefixes.map((v, i) => (
                          <span key={i} className="text-xs font-mono text-gray-400">{v}</span>
                        ))}
                        {overflowCount > 0 && (
                          <span className="text-xs text-gray-300">+{overflowCount} more</span>
                        )}
                      </div>
                    )}

                    {/* Expanded list */}
                    {isExpanded && (
                      <div className="mt-1.5 space-y-1 border-l-2 border-gray-200 pl-3 ml-1">
                        {rule.entries.map((entry, idx) => (
                          <div key={idx} className="flex items-center gap-2">
                            <span className={`px-1.5 py-0.5 text-xs rounded ${
                              entry.type === 'fqdn'
                                ? 'bg-green-100 text-green-700'
                                : 'bg-gray-100 text-gray-600'
                            }`}>
                              {entry.type === 'fqdn' ? 'FQDN' : 'IPv6'}
                            </span>
                            <span className="font-mono text-xs text-gray-700">{entry.value}</span>
                            {entry.service && (
                              <span className="text-xs text-gray-400">({entry.service})</span>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* Advertising router */}
                  <p className="text-xs text-gray-500">
                    <span className="font-medium">Advertising Router:</span>{' '}
                    <span className="font-semibold text-gray-700">{routerName(rule.nexthop)}</span>
                    {router && (
                      <span className="font-mono text-gray-400 ml-1">({router.address})</span>
                    )}
                  </p>
                </div>

                <button
                  onClick={() => onDeleteRule(rule.id)}
                  className="text-red-400 hover:text-red-600 flex-shrink-0 mt-0.5"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          );
        })}
      </div>

      {/* Add form (toggled) */}
      {showAddForm && (
        <div className="border border-blue-200 rounded-lg p-5 bg-blue-50">
          <div className="flex items-center justify-between mb-4">
            <h3 className="font-semibold text-gray-700 text-sm">New Policy Rule</h3>
            <button
              onClick={() => setShowAddForm(false)}
              className="text-gray-400 hover:text-gray-600"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          <div className="space-y-4">
            {/* Name / Comment */}
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Name / Comment</label>
              <input
                type="text"
                value={newRule.comment}
                onChange={e => onNewRuleChange({ ...newRule, comment: e.target.value })}
                placeholder="e.g., Microsoft 365 → Router-A"
                className="w-full px-3 py-2 border rounded-lg text-sm bg-white"
              />
            </div>

            {/* Prefix list */}
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-2">Prefix List</label>
              {newRule.entries.length > 0 && (
                <div className="mb-2 p-3 bg-white rounded-lg border space-y-1.5">
                  {newRule.entries.map((entry, idx) => (
                    <div key={idx} className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className={`px-1.5 py-0.5 text-xs rounded ${
                          entry.type === 'fqdn' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'
                        }`}>
                          {entry.type === 'fqdn' ? 'FQDN' : 'IPv6'}
                        </span>
                        <span className="font-mono text-xs text-gray-700">{entry.value}</span>
                        {entry.service && <span className="text-xs text-gray-400">({entry.service})</span>}
                      </div>
                      <button onClick={() => onRemoveEntry(idx)} className="text-red-400 hover:text-red-600">
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <div className="flex gap-2">
                <select
                  value={newRuleEntry.type}
                  onChange={e => onNewRuleEntryChange({ ...newRuleEntry, type: e.target.value as 'ipv6' | 'fqdn' })}
                  className="px-3 py-2 border rounded-lg text-sm bg-white"
                >
                  <option value="ipv6">IPv6 Prefix</option>
                  <option value="fqdn">FQDN Service</option>
                </select>
                {newRuleEntry.type === 'ipv6' ? (
                  <>
                    <input
                      type="text"
                      value={newRuleEntry.value}
                      onChange={e => onNewRuleEntryChange({ ...newRuleEntry, value: e.target.value })}
                      placeholder="2001:db8::/32 or ::/0 for default"
                      className="flex-1 px-3 py-2 border rounded-lg text-sm bg-white"
                      onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); onAddEntry(); } }}
                    />
                    <button
                      onClick={onAddEntry}
                      disabled={!newRuleEntry.value}
                      className="px-3 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 disabled:bg-gray-300 transition"
                    >
                      <Plus className="w-4 h-4" />
                    </button>
                  </>
                ) : (
                  <button
                    onClick={onOpenFqdnModal}
                    className="flex-1 flex items-center gap-2 px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 text-sm justify-center transition"
                  >
                    <Globe className="w-4 h-4" />
                    Select FQDN Service
                  </button>
                )}
              </div>
            </div>

            {/* Advertising router */}
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Advertising Router</label>
              <select
                value={newRule.nexthop}
                onChange={e => onNewRuleChange({ ...newRule, nexthop: e.target.value })}
                className="w-full px-3 py-2 border rounded-lg text-sm bg-white"
              >
                <option value="">Select router...</option>
                {routers.map(r => (
                  <option key={r.id} value={r.address}>{r.name} ({r.address})</option>
                ))}
              </select>
            </div>

            <div className="flex gap-2 pt-1">
              <button
                onClick={handleAddRule}
                disabled={newRule.entries.length === 0 || !newRule.nexthop}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-300 disabled:cursor-not-allowed text-sm transition"
              >
                <Plus className="w-4 h-4" />
                Add Policy Rule
              </button>
              <button
                onClick={() => setShowAddForm(false)}
                className="px-4 py-2 border rounded-lg hover:bg-white text-sm text-gray-600 transition"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
