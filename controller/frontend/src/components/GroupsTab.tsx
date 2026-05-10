import { useRef, useState } from 'react';
import { Plus, Trash2, ChevronDown, ChevronRight, Users, X, Pencil, Check } from 'lucide-react';
import type { Group, Rule, Neighbor, RouterConfig } from '../types';

interface GroupsTabProps {
  groups: Group[];
  rules: Rule[];
  routers: RouterConfig[];
  neighbors: Neighbor[];
  newGroup: { name: string };
  selectedNeighbors: string[];
  expandedGroups: Record<number, boolean>;
  onNewGroupChange: (group: { name: string }) => void;
  onSelectedNeighborsChange: (neighbors: string[]) => void;
  onAddGroup: () => void;
  onDeleteGroup: (id: number) => void;
  onUpdateGroupRules: (id: number, rules: number[]) => void;
  onToggleExpanded: (groupId: number) => void;
  onOpenNeighborModal: () => void;
}

/** Small LED dot for inline state summary */
function StateDot({ className }: { className: string }) {
  return <span className={`w-1.5 h-1.5 rounded-full inline-block flex-shrink-0 ${className}`} />;
}

export default function GroupsTab({
  groups,
  rules,
  routers,
  neighbors,
  newGroup,
  selectedNeighbors,
  expandedGroups,
  onNewGroupChange,
  onSelectedNeighborsChange,
  onAddGroup,
  onDeleteGroup,
  onUpdateGroupRules,
  onToggleExpanded,
  onOpenNeighborModal,
}: GroupsTabProps) {
  const manualMemberRef = useRef<HTMLInputElement>(null);
  const [showAddForm, setShowAddForm] = useState(false);
  const [hoveredRuleId, setHoveredRuleId] = useState<number | null>(null);
  const [editingRulesGroupId, setEditingRulesGroupId] = useState<number | null>(null);
  const [editingRules, setEditingRules] = useState<number[]>([]);

  const addManualMember = () => {
    const input = manualMemberRef.current;
    if (!input) return;
    const addr = input.value.trim();
    if (addr && !selectedNeighbors.includes(addr)) {
      onSelectedNeighborsChange([...selectedNeighbors, addr]);
      input.value = '';
    }
  };

  // The Nexthop field can hold either router.id (UUID) or router.address.
  const routerName = (nexthop: string) =>
    routers.find(r => r.id === nexthop || r.address === nexthop)?.name ?? nexthop;

  /** Compute REACHABLE / STALE / unknown counts for a group's members */
  const clientStateSummary = (members: string[]) => {
    let reachable = 0, stale = 0, unknown = 0;
    for (const id of members) {
      const state = neighbors.find(n => n.id === id)?.state;
      if (state === 'REACHABLE') reachable++;
      else if (state === 'STALE') stale++;
      else unknown++;
    }
    return { reachable, stale, unknown };
  };

  const startEditRules = (group: Group) => {
    setEditingRulesGroupId(group.id);
    setEditingRules([...group.rules]);
  };

  const cancelEditRules = () => {
    setEditingRulesGroupId(null);
    setEditingRules([]);
  };

  const saveEditRules = (groupId: number) => {
    onUpdateGroupRules(groupId, editingRules);
    setEditingRulesGroupId(null);
    setEditingRules([]);
  };

  const handleAddGroup = () => {
    onAddGroup();
    setShowAddForm(false);
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-base font-semibold text-gray-800">Policy Groups</h2>
        {!showAddForm && (
          <button
            onClick={() => setShowAddForm(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm transition"
          >
            <Plus className="w-3.5 h-3.5" />
            Add Policy Group
          </button>
        )}
      </div>

      {/* Group list */}
      <div className="space-y-2 mb-4">
        {groups.length === 0 && !showAddForm && (
          <p className="text-sm text-gray-400 py-6 text-center">No policy groups defined yet.</p>
        )}

        {groups.map(group => {
          const { reachable, stale, unknown } = clientStateSummary(group.members);
          const isEditingRules = editingRulesGroupId === group.id;
          return (
            <div key={group.id} className="border rounded-lg overflow-hidden">
              {/* Card header */}
              <div className="flex justify-between items-center px-4 py-3 bg-gray-50 border-b">
                <h3 className="font-semibold text-gray-800 text-sm">
                  {group.name}
                  <span className="ml-2 text-xs text-gray-400 font-mono font-normal">#{group.id}</span>
                </h3>
                <button
                  onClick={() => onDeleteGroup(group.id)}
                  className="text-red-400 hover:text-red-600"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>

              <div className="px-4 py-3 space-y-3">
                {/* Applied rules section */}
                <div className="rounded-lg border border-indigo-100 bg-indigo-50 p-3">
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-xs font-semibold text-indigo-700 uppercase tracking-wide">
                      Applied Rules
                    </span>
                    {!isEditingRules && (
                      <button
                        onClick={() => startEditRules(group)}
                        className="flex items-center gap-1 px-2 py-1 text-xs font-medium text-indigo-600 bg-white border border-indigo-200 rounded-md hover:bg-indigo-50 transition"
                      >
                        <Pencil className="w-3 h-3" />
                        Assign
                      </button>
                    )}
                  </div>

                  {isEditingRules ? (
                    <>
                      {rules.length === 0 ? (
                        <p className="text-xs text-gray-400">No policy rules defined.</p>
                      ) : (
                        <div className="space-y-1.5 mb-3">
                          {rules.map(rule => {
                            const isDefault = rule.entries.some(e => e.value === '::/0');
                            const checked = editingRules.includes(rule.id);
                            return (
                              <label
                                key={rule.id}
                                className={`flex items-start gap-3 p-2.5 rounded-lg border cursor-pointer transition bg-white ${
                                  checked ? 'border-blue-300' : 'border-transparent hover:border-gray-200'
                                }`}
                              >
                                <input
                                  type="checkbox"
                                  checked={checked}
                                  onChange={() => {
                                    setEditingRules(prev =>
                                      prev.includes(rule.id)
                                        ? prev.filter(r => r !== rule.id)
                                        : [...prev, rule.id]
                                    );
                                  }}
                                  className="rounded mt-0.5 flex-shrink-0"
                                />
                                <div className="min-w-0">
                                  <div className="flex items-center gap-2 mb-0.5 flex-wrap">
                                    <span className="text-sm font-medium text-gray-800">
                                      {rule.comment || `Rule #${rule.id}`}
                                    </span>
                                    <span className={`px-1.5 py-0.5 text-xs rounded font-medium ${
                                      isDefault ? 'bg-purple-100 text-purple-700' : 'bg-blue-100 text-blue-700'
                                    }`}>
                                      {isDefault ? 'Default Route' : 'Explicit'}
                                    </span>
                                  </div>
                                  <div className="text-xs text-gray-400 flex flex-wrap gap-x-3">
                                    <span>{rule.entries.length} prefix{rule.entries.length !== 1 ? 'es' : ''}</span>
                                    <span>→ {routerName(rule.nexthop)}</span>
                                  </div>
                                </div>
                              </label>
                            );
                          })}
                        </div>
                      )}
                      <div className="flex gap-2">
                        <button
                          onClick={() => saveEditRules(group.id)}
                          className="flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-xs transition"
                        >
                          <Check className="w-3 h-3" />
                          Save
                        </button>
                        <button
                          onClick={cancelEditRules}
                          className="px-3 py-1.5 border rounded-lg hover:bg-white text-xs text-gray-600 transition"
                        >
                          Cancel
                        </button>
                      </div>
                    </>
                  ) : group.rules.length === 0 ? (
                    <p className="text-xs text-indigo-400 italic">No rules assigned yet.</p>
                  ) : (
                    <div className="flex flex-wrap gap-1.5">
                      {group.rules.map(ruleId => {
                        const rule = rules.find(r => r.id === ruleId);
                        const label = rule?.comment || `#${ruleId}`;
                        const isDefault = rule?.entries.some(e => e.value === '::/0');
                        const isHovered = hoveredRuleId === ruleId;
                        return (
                          <div key={ruleId} className="relative">
                            <span
                              className={`inline-block px-2 py-1 text-xs rounded-md font-medium cursor-default select-none ${
                                !rule
                                  ? 'bg-red-100 text-red-600'
                                  : isDefault
                                  ? 'bg-purple-100 text-purple-700'
                                  : 'bg-blue-100 text-blue-700'
                              }`}
                              onMouseEnter={() => rule && setHoveredRuleId(ruleId)}
                              onMouseLeave={() => setHoveredRuleId(null)}
                            >
                              {label}{!rule && ' (deleted)'}
                            </span>
                            {isHovered && rule && (
                              <div className="absolute bottom-full left-0 mb-1 z-20 bg-white border rounded-lg shadow-xl p-3 w-56 text-xs pointer-events-none">
                                <p className="font-semibold text-gray-800 mb-1.5 truncate">{rule.comment || `Rule #${rule.id}`}</p>
                                <div className="space-y-1 text-gray-600">
                                  <p><span className="text-gray-400">Router:</span>{' '}<span className="font-medium">{routerName(rule.nexthop)}</span></p>
                                  <p><span className="text-gray-400">Prefixes:</span>{' '}<span className="font-medium">{rule.entries.length}</span></p>
                                  {rule.entries.slice(0, 3).map((e, i) => (
                                    <p key={i} className="font-mono text-gray-400 truncate">{e.value}</p>
                                  ))}
                                  {rule.entries.length > 3 && (
                                    <p className="text-gray-300">+{rule.entries.length - 3} more</p>
                                  )}
                                </div>
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>

                  {/* Clients — collapsible with state summary */}
                  <div>
                    <button
                      onClick={() => onToggleExpanded(group.id)}
                      className="flex items-center gap-1 text-blue-600 hover:text-blue-800 text-xs font-medium"
                    >
                      {expandedGroups[group.id]
                        ? <ChevronDown className="w-3.5 h-3.5" />
                        : <ChevronRight className="w-3.5 h-3.5" />}
                      Clients ({group.members.length})
                    </button>

                    {/* State summary when collapsed */}
                    {!expandedGroups[group.id] && group.members.length > 0 && (
                      <div className="flex items-center gap-3 mt-1 ml-5 text-xs text-gray-500">
                        {reachable > 0 && (
                          <span className="flex items-center gap-1">
                            <StateDot className="bg-green-500" />{reachable}
                          </span>
                        )}
                        {stale > 0 && (
                          <span className="flex items-center gap-1">
                            <StateDot className="bg-yellow-400" />{stale}
                          </span>
                        )}
                        {unknown > 0 && (
                          <span className="flex items-center gap-1">
                            <StateDot className="bg-gray-400" />{unknown}
                          </span>
                        )}
                      </div>
                    )}

                    {/* Expanded client detail */}
                    {expandedGroups[group.id] && (
                      <div className="mt-2 space-y-1 pl-5">
                        {group.members.map((memberId, idx) => {
                          const neighbor = neighbors.find(n => n.id === memberId);
                          const state = neighbor?.state ?? 'unknown';
                          const dotColor =
                            state === 'REACHABLE' ? 'bg-green-500'
                            : state === 'STALE' ? 'bg-yellow-400'
                            : 'bg-gray-400';
                          return (
                            <div key={idx} className="text-xs bg-gray-50 px-3 py-2 rounded flex items-start gap-2">
                              <StateDot className={`${dotColor} mt-1`} />
                              <div>
                                <div className="font-mono text-gray-800">{memberId}</div>
                                {neighbor && (
                                  <div className="text-gray-400 flex flex-wrap gap-x-3 mt-0.5">
                                    <span>MAC: {neighbor.lladdr}</span>
                                    <span>If: {neighbor.ifname}</span>
                                  </div>
                                )}
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </div>
              </div>
            </div>
          );
        })}
      </div>

      {/* Add form (toggled) */}
      {showAddForm && (
        <div className="border border-blue-200 rounded-lg p-5 bg-blue-50">
          <div className="flex items-center justify-between mb-4">
            <h3 className="font-semibold text-gray-700 text-sm">New Policy Group</h3>
            <button
              onClick={() => setShowAddForm(false)}
              className="text-gray-400 hover:text-gray-600"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          <div className="space-y-4">
            {/* Group name */}
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Group Name</label>
              <input
                type="text"
                value={newGroup.name}
                onChange={e => onNewGroupChange({ ...newGroup, name: e.target.value })}
                placeholder="e.g., Room-101"
                className="w-full px-3 py-2 border rounded-lg text-sm bg-white"
              />
            </div>

            {/* Client selection */}
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-2">Clients</label>
              <div className="space-y-2">
                <div className="flex gap-2">
                  <input
                    ref={manualMemberRef}
                    type="text"
                    placeholder="IPv6 address (e.g., fe80::1)"
                    className="flex-1 px-3 py-2 border rounded-lg text-sm bg-white"
                    onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addManualMember(); } }}
                  />
                  <button
                    onClick={addManualMember}
                    className="px-3 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition"
                  >
                    <Plus className="w-4 h-4" />
                  </button>
                </div>
                <button
                  onClick={onOpenNeighborModal}
                  className="flex items-center gap-2 px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 w-full text-sm justify-center transition"
                >
                  <Users className="w-4 h-4" />
                  Select from ND Cache
                </button>
              </div>

              {selectedNeighbors.length > 0 && (
                <div className="mt-3 p-3 bg-white rounded-lg border">
                  <p className="text-xs font-medium text-gray-600 mb-2">
                    Selected ({selectedNeighbors.length})
                  </p>
                  <div className="flex flex-wrap gap-1">
                    {selectedNeighbors.map(id => (
                      <span key={id} className="inline-flex items-center gap-1 px-2 py-0.5 bg-blue-100 text-blue-700 rounded text-xs font-mono">
                        {id}
                        <button
                          onClick={() => onSelectedNeighborsChange(selectedNeighbors.filter(n => n !== id))}
                          className="text-blue-500 hover:text-blue-800 ml-0.5"
                        >
                          ×
                        </button>
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>

            <div className="flex gap-2 pt-1">
              <button
                onClick={handleAddGroup}
                disabled={!newGroup.name || selectedNeighbors.length === 0}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-300 disabled:cursor-not-allowed text-sm transition"
              >
                <Plus className="w-4 h-4" />
                Add Policy Group
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
