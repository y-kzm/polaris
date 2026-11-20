import { useState } from 'react';
import { Pencil, Trash2 } from 'lucide-react';
import type { RouterConfig, RouterDeleteResult, RouterInput } from '../../types';
import { ApiError, errorMessage } from '../../utils/api';
import { INACTIVE_EXPLANATION } from '../../utils/format';
import RouterForm from '../RouterForm';
import ConfirmModal from './ConfirmModal';

interface RouterConfigModalProps {
  routers: RouterConfig[];
  /** Creates (no id) or replaces a router. Rejects with the backend's message. */
  onSaveRouter: (input: RouterInput, id?: string) => Promise<void>;
  /** Decommissions a router; `force` removes it even if its policy state could not be withdrawn. */
  onDeleteRouter: (id: string, force: boolean) => Promise<RouterDeleteResult>;
  onClose: () => void;
}

/** A removal that waits for the administrator's confirmation. */
interface PendingRemoval {
  router: RouterConfig;
  /** Set once the backend refused and offered force=1: its message. */
  forceOffer?: string;
}

export default function RouterConfigModal({ routers, onSaveRouter, onDeleteRouter, onClose }: RouterConfigModalProps) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [removal, setRemoval] = useState<PendingRemoval | null>(null);
  const [removing, setRemoving] = useState(false);
  const [notice, setNotice] = useState<{ type: 'error' | 'warning' | 'success'; text: string } | null>(null);

  const editing = routers.find(r => r.id === editingId);

  const remove = async (force: boolean) => {
    if (!removal) return;
    const { router } = removal;
    setRemoving(true);
    try {
      const result = await onDeleteRouter(router.id, force);
      setRemoval(null);
      if (editingId === router.id) setEditingId(null);
      setNotice(result.warning
        ? { type: 'warning', text: `${router.name} was removed, but: ${result.warning}` }
        : { type: 'success', text: `${router.name} was removed; ${result.withdrawn_hosts} host(s) were sent a withdrawal RA.` });
    } catch (e) {
      const message = errorMessage(e);
      // 409 either names the rules that still use the router, or says that
      // its policy state could not be withdrawn and offers force=1.
      if (!force && e instanceof ApiError && e.status === 409 && message.includes('force=1')) {
        setRemoval({ router, forceOffer: message });
      } else {
        setRemoval(null);
        setNotice({ type: 'error', text: `${router.name} was not removed: ${message}` });
      }
    } finally {
      setRemoving(false);
    }
  };

  const noticeStyle = {
    error: 'text-red-700 bg-red-50 border-red-200',
    warning: 'text-yellow-800 bg-yellow-50 border-yellow-300',
    success: 'text-green-800 bg-green-50 border-green-200',
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-xl font-bold">Router Configuration</h2>
          <p className="text-sm text-gray-600 mt-1">Add, edit or remove site-exit routers</p>
        </div>

        <div className="overflow-y-auto max-h-[60vh] p-6">
          {notice && (
            <p className={`mb-4 text-sm border rounded-lg px-3 py-2 break-words ${noticeStyle[notice.type]}`}>
              {notice.text}
            </p>
          )}

          {/* Current routers list */}
          <div className="space-y-4 mb-6">
            <h3 className="font-semibold">Current Routers</h3>
            {routers.length === 0 ? (
              <div className="text-center py-8 text-gray-500">
                No routers configured. Add a router below to get started.
              </div>
            ) : (
              routers.map(router => (
                <div
                  key={router.id}
                  className={`flex items-start justify-between p-4 border rounded-lg ${
                    router.id === editingId ? 'border-blue-300 bg-blue-50' : ''
                  }`}
                >
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-2 flex-wrap">
                      <span className="font-semibold">{router.name}</span>
                      <span className={`px-2 py-1 text-xs rounded ${
                        router.status === 'active'
                          ? 'bg-green-100 text-green-700'
                          : 'bg-gray-200 text-gray-700'
                      }`}>
                        {router.status === 'active' ? 'Active: takes part in the policy' : INACTIVE_EXPLANATION}
                      </span>
                    </div>
                    <div className="text-sm text-gray-600 space-y-1">
                      <div>Address: <span className="font-mono">{router.address}</span></div>
                      <div>Interface: <span className="font-mono">{router.interface}</span></div>
                      <div>Policy source: <span className="font-mono">{router.policy_source_address || '(baseline address)'}</span></div>
                      <div className="text-xs mt-2 pt-2 border-t">
                        <div className="grid grid-cols-2 gap-x-4 gap-y-1">
                          <div>RA Interval: {router.ra_interval_milliseconds}ms</div>
                          <div>Hop Limit: {router.current_hop_limit}</div>
                          <div>Router Lifetime: {router.router_lifetime_seconds}s</div>
                          <div>Reachable Time: {router.reachable_time_milliseconds}ms</div>
                          <div>Retransmit Time: {router.retransmit_time_milliseconds}ms</div>
                          <div>
                            Flags:
                            {router.managed ? ' M' : ''}
                            {router.other ? ' O' : ''}
                            {!router.managed && !router.other ? ' None' : ''}
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-3 ml-4">
                    <button
                      onClick={() => { setEditingId(router.id); setNotice(null); }}
                      className="text-gray-500 hover:text-blue-600"
                      title="Edit router"
                    >
                      <Pencil className="w-5 h-5" />
                    </button>
                    <button
                      onClick={() => { setRemoval({ router }); setNotice(null); }}
                      className="text-red-600 hover:text-red-800"
                      title="Remove router"
                    >
                      <Trash2 className="w-5 h-5" />
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>

          {/* Add or edit form; `key` gives each router its own pre-filled form */}
          <div className="border-t pt-6">
            <h3 className="font-semibold mb-4">{editing ? `Edit ${editing.name}` : 'Add New Router'}</h3>
            <RouterForm
              key={editing ? editing.id : 'new'}
              initial={editing}
              onSubmit={async input => {
                await onSaveRouter(input, editing?.id);
                setEditingId(null);
                setNotice(null);
              }}
              onCancel={editing ? () => setEditingId(null) : undefined}
            />
          </div>
        </div>

        {/* Modal footer */}
        <div className="p-6 border-t flex justify-end gap-3">
          <button onClick={onClose} className="px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700">
            Close
          </button>
        </div>
      </div>

      {removal && !removal.forceOffer && (
        <ConfirmModal
          title={`Remove ${removal.router.name}?`}
          message="The controller first withdraws the router's policy RAs from the hosts, then removes it from the registry. A router that is still the next hop of a rule is not removed."
          confirmLabel="Remove router"
          variant="danger"
          busy={removing}
          onConfirm={() => remove(false)}
          onCancel={() => setRemoval(null)}
        />
      )}
      {removal?.forceOffer && (
        <ConfirmModal
          title={`${removal.router.name} was not removed`}
          message={
            <>
              <span className="block text-red-700 mb-3">{removal.forceOffer}</span>
              <span className="block">
                Remove it anyway? Its policy state stays on the router and on the hosts until the
                lifetimes expire, and the controller can no longer withdraw it.
              </span>
            </>
          }
          confirmLabel="Remove anyway"
          variant="danger"
          busy={removing}
          onConfirm={() => remove(true)}
          onCancel={() => setRemoval(null)}
        />
      )}
    </div>
  );
}
