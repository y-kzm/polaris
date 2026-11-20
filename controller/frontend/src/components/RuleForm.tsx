import { useState } from 'react';
import { Check, Globe, Plus, X } from 'lucide-react';
import type { Entry, RouterConfig, Rule, RuleInput, ServiceCatalog } from '../types';
import { errorMessage } from '../utils/api';
import EntryRow from './EntryRow';
import FqdnModal from './modals/FqdnModal';

interface RuleFormProps {
  /** The rule being edited; absent for a new rule. */
  initial?: Rule;
  routers: RouterConfig[];
  catalog: ServiceCatalog;
  /** Resolves once the backend accepted the rule; rejects with its message. */
  onSubmit: (input: RuleInput) => Promise<void>;
  onCancel: () => void;
}

/** Form for a new rule or for editing one. It stays open until the backend accepted it. */
export default function RuleForm({ initial, routers, catalog, onSubmit, onCancel }: RuleFormProps) {
  const [comment, setComment] = useState(initial?.comment ?? '');
  const [entries, setEntries] = useState<Entry[]>(initial?.entries ?? []);
  const [nexthop, setNexthop] = useState(initial?.nexthop ?? '');
  const [entryType, setEntryType] = useState<Entry['type']>('ipv6');
  const [prefix, setPrefix] = useState('');
  const [showPicker, setShowPicker] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const addPrefix = () => {
    const value = prefix.trim();
    if (!value) return;
    setEntries(prev => [...prev, { type: 'ipv6', value }]);
    setPrefix('');
  };

  const addService = (name: string) => {
    setShowPicker(false);
    if (!name) return;
    setEntries(prev =>
      prev.some(e => e.type === 'service' && e.value === name) ? prev : [...prev, { type: 'service', value: name }]);
  };

  // A prefix that was typed but not added yet belongs to the rule too.
  const pending = entryType === 'ipv6' ? prefix.trim() : '';
  const allEntries: Entry[] = pending ? [...entries, { type: 'ipv6', value: pending }] : entries;

  const submit = async () => {
    setSaving(true);
    setError(null);
    try {
      await onSubmit({ comment, entries: allEntries, nexthop });
    } catch (e) {
      setError(errorMessage(e));
      setSaving(false);
    }
  };

  // A rule may name its router by address or by ID; keep what it has.
  const optionValue = (r: RouterConfig) => (initial && initial.nexthop === r.id ? r.id : r.address);
  const nexthopKnown = nexthop === '' || routers.some(r => optionValue(r) === nexthop);

  return (
    <div className="border border-blue-200 rounded-lg p-5 bg-blue-50">
      <div className="flex items-center justify-between mb-4">
        <h3 className="font-semibold text-gray-700 text-sm">
          {initial ? `Edit Policy Rule #${initial.id}` : 'New Policy Rule'}
        </h3>
        <button onClick={onCancel} className="text-gray-400 hover:text-gray-600">
          <X className="w-4 h-4" />
        </button>
      </div>

      <div className="space-y-4">
        {/* Name / Comment */}
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Name / Comment</label>
          <input
            type="text"
            value={comment}
            onChange={e => setComment(e.target.value)}
            placeholder="e.g., Microsoft 365 → Router-A"
            className="w-full px-3 py-2 border rounded-lg text-sm bg-white"
          />
        </div>

        {/* Entries */}
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-2">Prefix List</label>
          {entries.length > 0 && (
            <div className="mb-2 p-3 bg-white rounded-lg border space-y-1.5">
              {entries.map((entry, idx) => (
                <EntryRow
                  key={`${entry.type}|${entry.value}|${idx}`}
                  entry={entry}
                  fetchEndpoints={catalog.fetchEndpoints}
                  onRemove={() => setEntries(prev => prev.filter((_, i) => i !== idx))}
                />
              ))}
            </div>
          )}
          <div className="flex gap-2">
            <select
              value={entryType}
              onChange={e => setEntryType(e.target.value === 'service' ? 'service' : 'ipv6')}
              className="px-3 py-2 border rounded-lg text-sm bg-white"
            >
              <option value="ipv6">IPv6 Prefix</option>
              <option value="service">Service</option>
            </select>
            {entryType === 'ipv6' ? (
              <>
                <input
                  type="text"
                  value={prefix}
                  onChange={e => setPrefix(e.target.value)}
                  placeholder="2001:db8::/32 or ::/0 for default"
                  className="flex-1 px-3 py-2 border rounded-lg text-sm bg-white"
                  onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addPrefix(); } }}
                />
                <button
                  onClick={addPrefix}
                  disabled={!prefix.trim()}
                  className="px-3 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 disabled:bg-gray-300 transition"
                >
                  <Plus className="w-4 h-4" />
                </button>
              </>
            ) : (
              <button
                onClick={() => setShowPicker(true)}
                className="flex-1 flex items-center gap-2 px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 text-sm justify-center transition"
              >
                <Globe className="w-4 h-4" />
                Select Service
              </button>
            )}
          </div>
          {entryType === 'service' && (
            <p className="text-xs text-gray-500 mt-1">
              A service entry names the service. Its prefixes are looked up by the controller at every apply.
            </p>
          )}
        </div>

        {/* Advertising router */}
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Advertising Router</label>
          <select
            value={nexthop}
            onChange={e => setNexthop(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg text-sm bg-white"
          >
            <option value="">Select router...</option>
            {!nexthopKnown && <option value={nexthop}>{nexthop} (not a registered router)</option>}
            {routers.map(r => (
              <option key={r.id} value={optionValue(r)}>
                {r.name} ({r.address}){r.status === 'inactive' ? ' — inactive, excluded from the policy' : ''}
              </option>
            ))}
          </select>
        </div>

        {error && (
          <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
            {error}
          </p>
        )}

        <div className="flex gap-2 pt-1">
          <button
            onClick={submit}
            disabled={saving || allEntries.length === 0 || !nexthop}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-300 disabled:cursor-not-allowed text-sm transition"
          >
            {initial ? <Check className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            {saving ? 'Saving...' : initial ? 'Save Policy Rule' : 'Add Policy Rule'}
          </button>
          <button
            onClick={onCancel}
            className="px-4 py-2 border rounded-lg hover:bg-white text-sm text-gray-600 transition"
          >
            Cancel
          </button>
        </div>
      </div>

      {showPicker && (
        <FqdnModal catalog={catalog} onSelectService={addService} onClose={() => setShowPicker(false)} />
      )}
    </div>
  );
}
