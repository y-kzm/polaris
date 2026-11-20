import { useState } from 'react';
import { Check, Plus } from 'lucide-react';
import type { RouterConfig, RouterInput } from '../types';
import { errorMessage } from '../utils/api';
import { INACTIVE_EXPLANATION } from '../utils/format';

const EMPTY_ROUTER: RouterInput = {
  name: '',
  address: '',
  interface: 'eth0',
  policy_source_address: '',
  ra_interval_milliseconds: 3000,
  current_hop_limit: 64,
  managed: false,
  other: false,
  router_lifetime_seconds: 1800,
  reachable_time_milliseconds: 0,
  retransmit_time_milliseconds: 0,
  status: 'active',
};

/** Every field of a router that the form edits, taken from the stored router. */
function toInput(router: RouterConfig): RouterInput {
  return {
    name: router.name,
    address: router.address,
    interface: router.interface,
    policy_source_address: router.policy_source_address ?? '',
    ra_interval_milliseconds: router.ra_interval_milliseconds,
    current_hop_limit: router.current_hop_limit,
    managed: router.managed,
    other: router.other,
    router_lifetime_seconds: router.router_lifetime_seconds,
    reachable_time_milliseconds: router.reachable_time_milliseconds,
    retransmit_time_milliseconds: router.retransmit_time_milliseconds,
    status: router.status === 'inactive' ? 'inactive' : 'active',
  };
}

type NumericField =
  | 'ra_interval_milliseconds'
  | 'current_hop_limit'
  | 'router_lifetime_seconds'
  | 'reachable_time_milliseconds'
  | 'retransmit_time_milliseconds';

const NUMERIC_FIELDS: { field: NumericField; label: string; placeholder: string }[] = [
  { field: 'ra_interval_milliseconds', label: 'RA Interval (ms)', placeholder: '3000' },
  { field: 'current_hop_limit', label: 'Current Hop Limit', placeholder: '64' },
  { field: 'router_lifetime_seconds', label: 'Router Lifetime (s)', placeholder: '1800' },
  { field: 'reachable_time_milliseconds', label: 'Reachable Time (ms)', placeholder: '0' },
  { field: 'retransmit_time_milliseconds', label: 'Retransmit Time (ms)', placeholder: '0' },
];

interface RouterFormProps {
  /** The router being edited; absent for a new router. */
  initial?: RouterConfig;
  /** Resolves once the backend accepted the router; rejects with its message. */
  onSubmit: (input: RouterInput) => Promise<void>;
  /** Present when editing: leaves the form without saving. */
  onCancel?: () => void;
}

/** Form for a new router or for editing one. It keeps its content until the backend accepted it. */
export default function RouterForm({ initial, onSubmit, onCancel }: RouterFormProps) {
  const [form, setForm] = useState<RouterInput>(initial ? toInput(initial) : EMPTY_ROUTER);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (patch: Partial<RouterInput>) => setForm(prev => ({ ...prev, ...patch }));

  const submit = async () => {
    setSaving(true);
    setError(null);
    try {
      await onSubmit(form);
      if (!initial) setForm(EMPTY_ROUTER);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <label className="block text-sm font-medium mb-1">Router Name</label>
        <input
          type="text"
          value={form.name}
          onChange={e => set({ name: e.target.value })}
          placeholder="Router(x)"
          className="w-full px-3 py-2 border rounded-lg"
        />
      </div>
      <div>
        <label className="block text-sm font-medium mb-1">IPv6 Address</label>
        <input
          type="text"
          value={form.address}
          onChange={e => set({ address: e.target.value })}
          placeholder="2001:db8::1"
          className="w-full px-3 py-2 border rounded-lg"
        />
      </div>
      <div>
        <label className="block text-sm font-medium mb-1">Interface (for sending RAs)</label>
        <input
          type="text"
          value={form.interface}
          onChange={e => set({ interface: e.target.value })}
          placeholder="eth0"
          className="w-full px-3 py-2 border rounded-lg"
        />
      </div>
      <div>
        <label className="block text-sm font-medium mb-1">Policy RA source address</label>
        <input
          type="text"
          value={form.policy_source_address}
          onChange={e => set({ policy_source_address: e.target.value })}
          placeholder="fe80::face:1"
          className="w-full px-3 py-2 border rounded-lg"
        />
        <p className="text-xs text-gray-500 mt-1">
          A second link-local address on the interface, used only for policy RAs so they
          do not overwrite the baseline RA on the hosts. Leave empty to share the baseline address.
        </p>
      </div>
      <div>
        <label className="block text-sm font-medium mb-1">Status</label>
        <select
          value={form.status}
          onChange={e => set({ status: e.target.value === 'inactive' ? 'inactive' : 'active' })}
          className="w-full px-3 py-2 border rounded-lg bg-white"
        >
          <option value="active">Active: takes part in the policy</option>
          <option value="inactive">{INACTIVE_EXPLANATION}</option>
        </select>
        <p className="text-xs text-gray-500 mt-1">
          An inactive router that cannot be reached does not hold back the other routers. Use it
          for a router that is down or under maintenance.
        </p>
      </div>

      <div className="border-t pt-4 mt-4">
        <h4 className="font-semibold mb-3 text-sm">Interface Configuration</h4>
        <div className="grid grid-cols-2 gap-4">
          {NUMERIC_FIELDS.map(({ field, label, placeholder }) => (
            <div key={field}>
              <label className="block text-xs font-medium mb-1">{label}</label>
              <input
                type="number"
                value={form[field]}
                onChange={e => set({ [field]: parseInt(e.target.value, 10) || 0 })}
                placeholder={placeholder}
                className="w-full px-3 py-2 border rounded-lg text-sm"
              />
            </div>
          ))}
        </div>
        <div className="mt-3 space-y-2">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={form.managed}
              onChange={e => set({ managed: e.target.checked })}
              className="rounded"
            />
            <span className="text-sm">Managed Address Configuration (M flag)</span>
          </label>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={form.other}
              onChange={e => set({ other: e.target.checked })}
              className="rounded"
            />
            <span className="text-sm">Other Configuration (O flag)</span>
          </label>
        </div>
      </div>

      {error && (
        <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
          {error}
        </p>
      )}

      <div className="flex gap-2">
        <button
          onClick={submit}
          disabled={saving || !form.name.trim() || !form.address.trim()}
          className="flex-1 flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-400 disabled:cursor-not-allowed justify-center"
        >
          {initial ? <Check className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
          {saving ? 'Saving...' : initial ? 'Save Router' : 'Add Router'}
        </button>
        {onCancel && (
          <button onClick={onCancel} className="px-4 py-2 border rounded-lg hover:bg-gray-50">
            Cancel
          </button>
        )}
      </div>
    </div>
  );
}
