import { useState } from 'react';
import { Plus, Trash2, ChevronDown, ChevronRight, Pencil } from 'lucide-react';
import type { Rule, RouterConfig, Group, RuleInput, ServiceCatalog } from '../types';
import EntryRow from './EntryRow';
import RuleForm from './RuleForm';

interface RulesTabProps {
  rules: Rule[];
  routers: RouterConfig[];
  groups: Group[];
  catalog: ServiceCatalog;
  /** Creates (no id) or replaces a rule. Rejects with the backend's message. */
  onSaveRule: (input: RuleInput, id?: number) => Promise<void>;
  onDeleteRule: (id: number) => void;
}

export default function RulesTab({ rules, routers, groups, catalog, onSaveRule, onDeleteRule }: RulesTabProps) {
  const [showAddForm, setShowAddForm] = useState(false);
  const [editingRuleId, setEditingRuleId] = useState<number | null>(null);
  const [expandedRules, setExpandedRules] = useState<Record<number, boolean>>({});

  const onToggleExpanded = (ruleId: number) =>
    setExpandedRules(prev => ({ ...prev, [ruleId]: !prev[ruleId] }));

  // The Nexthop field can hold either router.id (UUID) or router.address.
  const findRouter = (nexthop: string) =>
    routers.find(r => r.id === nexthop || r.address === nexthop);

  const routerName = (nexthop: string) => {
    const r = findRouter(nexthop);
    return r ? r.name : nexthop;
  };

  const usedByGroups = (ruleId: number) =>
    groups.filter(g => g.rules.includes(ruleId));

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
          if (editingRuleId === rule.id) {
            return (
              <RuleForm
                key={rule.id}
                initial={rule}
                routers={routers}
                catalog={catalog}
                onSubmit={async input => { await onSaveRule(input, rule.id); setEditingRuleId(null); }}
                onCancel={() => setEditingRuleId(null)}
              />
            );
          }
          const isDefault = rule.entries.some(e => e.type === 'ipv6' && e.value === '::/0');
          const title = rule.comment || `Rule #${rule.id}`;
          const router = findRouter(rule.nexthop);
          const usingGroups = usedByGroups(rule.id);
          const isExpanded = expandedRules[rule.id];

          // Inline prefix preview shown when collapsed
          const previewPrefixes = rule.entries.slice(0, 2)
            .map(e => (e.type === 'service' ? `service: ${e.value}` : e.value));
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
                          <EntryRow
                            key={`${entry.type}|${entry.value}|${idx}`}
                            entry={entry}
                            fetchEndpoints={catalog.fetchEndpoints}
                          />
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
                    {router?.status === 'inactive' && (
                      <span className="ml-2 px-1.5 py-0.5 rounded bg-gray-200 text-gray-600">
                        inactive: this rule is skipped
                      </span>
                    )}
                  </p>
                </div>

                <div className="flex items-center gap-3 flex-shrink-0 mt-0.5">
                  <button
                    onClick={() => setEditingRuleId(rule.id)}
                    className="text-gray-400 hover:text-blue-600"
                    title="Edit rule"
                  >
                    <Pencil className="w-4 h-4" />
                  </button>
                  <button
                    onClick={() => onDeleteRule(rule.id)}
                    className="text-red-400 hover:text-red-600"
                    title="Delete rule (it is also removed from every group)"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>
          );
        })}
      </div>

      {/* Add form (toggled); it closes only after the backend accepted the rule */}
      {showAddForm && (
        <RuleForm
          routers={routers}
          catalog={catalog}
          onSubmit={async input => { await onSaveRule(input); setShowAddForm(false); }}
          onCancel={() => setShowAddForm(false)}
        />
      )}
    </div>
  );
}
