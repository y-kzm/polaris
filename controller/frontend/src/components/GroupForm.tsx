import { useState } from 'react';
import { Check, Plus, Users, X } from 'lucide-react';
import type { Group, Neighbor, SourcesStatus } from '../types';
import { errorMessage } from '../utils/api';
import { looksLinkLocal } from '../utils/format';
import NeighborModal from './modals/NeighborModal';

const LINK_LOCAL_HINT = 'Hosts are identified by their link-local IPv6 address (fe80::/10).';

interface GroupFormProps {
  /** The group being edited; absent for a new group. */
  initial?: Group;
  neighbors: Neighbor[];
  sources: SourcesStatus | null;
  sourceDescription: string;
  /** Resolves once the backend accepted the group; rejects with its message. */
  onSubmit: (input: { name: string; members: string[] }) => Promise<void>;
  onCancel: () => void;
}

/** Form for a new group or for a group's name and members. It stays open until the backend accepted it. */
export default function GroupForm({ initial, neighbors, sources, sourceDescription, onSubmit, onCancel }: GroupFormProps) {
  const [name, setName] = useState(initial?.name ?? '');
  const [members, setMembers] = useState<string[]>(initial?.members ?? []);
  const [manual, setManual] = useState('');
  const [picker, setPicker] = useState<{ before: string[] } | null>(null);
  const [lastCheckedIndex, setLastCheckedIndex] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const typed = manual.trim();

  const addManual = () => {
    if (!typed) return;
    setMembers(prev => (prev.includes(typed) ? prev : [...prev, typed]));
    setManual('');
  };

  const submit = async () => {
    // An address that was typed but not added yet belongs to the group too.
    const all = typed && !members.includes(typed) ? [...members, typed] : members;
    setSaving(true);
    setError(null);
    try {
      await onSubmit({ name, members: all });
    } catch (e) {
      setError(errorMessage(e));
      setSaving(false);
    }
  };

  const doubtful = members.filter(m => !looksLinkLocal(m));

  return (
    <div className="border border-blue-200 rounded-lg p-5 bg-blue-50">
      <div className="flex items-center justify-between mb-4">
        <h3 className="font-semibold text-gray-700 text-sm">
          {initial ? `Edit Policy Group #${initial.id}` : 'New Policy Group'}
        </h3>
        <button onClick={onCancel} className="text-gray-400 hover:text-gray-600">
          <X className="w-4 h-4" />
        </button>
      </div>

      <div className="space-y-4">
        {/* Group name */}
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Group Name</label>
          <input
            type="text"
            value={name}
            onChange={e => setName(e.target.value)}
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
                type="text"
                value={manual}
                onChange={e => setManual(e.target.value)}
                placeholder="Link-local IPv6 address (e.g., fe80::1)"
                className={`flex-1 px-3 py-2 border rounded-lg text-sm bg-white ${
                  typed && !looksLinkLocal(typed) ? 'border-yellow-400' : ''
                }`}
                onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addManual(); } }}
              />
              <button
                onClick={addManual}
                disabled={!typed}
                className="px-3 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-300 transition"
              >
                <Plus className="w-4 h-4" />
              </button>
            </div>
            {typed && !looksLinkLocal(typed) && (
              <p className="text-xs text-yellow-700">
                This does not look like a link-local address. {LINK_LOCAL_HINT}
              </p>
            )}
            <button
              onClick={() => setPicker({ before: members })}
              className="flex items-center gap-2 px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 w-full text-sm justify-center transition"
            >
              <Users className="w-4 h-4" />
              Select from ND Cache
            </button>
          </div>

          {members.length > 0 && (
            <div className="mt-3 p-3 bg-white rounded-lg border">
              <p className="text-xs font-medium text-gray-600 mb-2">Selected ({members.length})</p>
              <div className="flex flex-wrap gap-1">
                {members.map(id => (
                  <span
                    key={id}
                    className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-mono ${
                      looksLinkLocal(id) ? 'bg-blue-100 text-blue-700' : 'bg-yellow-100 text-yellow-800'
                    }`}
                  >
                    {id}
                    <button
                      onClick={() => setMembers(prev => prev.filter(m => m !== id))}
                      className="hover:text-black ml-0.5"
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
              {doubtful.length > 0 && (
                <p className="text-xs text-yellow-700 mt-2">
                  {doubtful.length} address(es) do not look link-local and will probably be refused. {LINK_LOCAL_HINT}
                </p>
              )}
            </div>
          )}
        </div>

        {error && (
          <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
            {error}
          </p>
        )}

        <div className="flex gap-2 pt-1">
          <button
            onClick={submit}
            disabled={saving || !name.trim()}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-300 disabled:cursor-not-allowed text-sm transition"
          >
            {initial ? <Check className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
            {saving ? 'Saving...' : initial ? 'Save Policy Group' : 'Add Policy Group'}
          </button>
          <button
            onClick={onCancel}
            className="px-4 py-2 border rounded-lg hover:bg-white text-sm text-gray-600 transition"
          >
            Cancel
          </button>
        </div>
      </div>

      {picker && (
        <NeighborModal
          neighbors={neighbors}
          selectedNeighbors={members}
          lastCheckedIndex={lastCheckedIndex}
          sourceDescription={sourceDescription}
          sources={sources}
          onSelectionChange={setMembers}
          onLastCheckedIndexChange={setLastCheckedIndex}
          onCancel={() => { setMembers(picker.before); setPicker(null); }}
          onClose={() => setPicker(null)}
        />
      )}
    </div>
  );
}
