import { Plus, Trash2 } from 'lucide-react';
import type { RouterConfig } from '../../types';

interface RouterConfigModalProps {
  routers: RouterConfig[];
  newRouter: Omit<RouterConfig, 'id' | 'status'>;
  onNewRouterChange: (router: Omit<RouterConfig, 'id' | 'status'>) => void;
  onAddRouter: () => void;
  onDeleteRouter: (id: string) => void;
  onClose: () => void;
}

export default function RouterConfigModal({
  routers,
  newRouter,
  onNewRouterChange,
  onAddRouter,
  onDeleteRouter,
  onClose,
}: RouterConfigModalProps) {
  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-xl font-bold">Router Configuration</h2>
          <p className="text-sm text-gray-600 mt-1">
            Add or remove site-exit routers
          </p>
        </div>

        <div className="overflow-y-auto max-h-[60vh] p-6">
          {/* Current routers list */}
          <div className="space-y-4 mb-6">
            <h3 className="font-semibold">Current Routers</h3>
            {routers.length === 0 ? (
              <div className="text-center py-8 text-gray-500">
                No routers configured. Add a router below to get started.
              </div>
            ) : (
              routers.map(router => (
                <div key={router.id} className="flex items-start justify-between p-4 border rounded-lg">
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-2">
                      <span className="font-semibold">{router.name}</span>
                      <span className={`px-2 py-1 text-xs rounded ${
                        router.status === 'active'
                          ? 'bg-green-100 text-green-700'
                          : 'bg-red-100 text-red-700'
                      }`}>
                        {router.status}
                      </span>
                    </div>
                    <div className="text-sm text-gray-600 space-y-1">
                      <div>Address: <span className="font-mono">{router.address}</span></div>
                      <div>Interface: <span className="font-mono">{router.interface}</span></div>
                      <div className="text-xs mt-2 pt-2 border-t">
                        <div className="grid grid-cols-2 gap-x-4 gap-y-1">
                          <div>RA Interval: {router.ra_interval_milliseconds || 3000}ms</div>
                          <div>Hop Limit: {router.current_hop_limit || 64}</div>
                          <div>Router Lifetime: {router.router_lifetime_seconds || 1800}s</div>
                          <div>Reachable Time: {router.reachable_time_milliseconds || 0}ms</div>
                          <div>Retransmit Time: {router.retransmit_time_milliseconds || 0}ms</div>
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
                  {/* Delete router button */}
                  <button
                    onClick={() => onDeleteRouter(router.id)}
                    className="text-red-600 hover:text-red-800 ml-4"
                  >
                    <Trash2 className="w-5 h-5" />
                  </button>
                </div>
              ))
            )}
          </div>

          {/* Add new router form */}
          <div className="border-t pt-6">
            <h3 className="font-semibold mb-4">Add New Router</h3>
            <div className="space-y-4">
              {/* Router name */}
              <div>
                <label className="block text-sm font-medium mb-1">Router Name</label>
                <input
                  type="text"
                  value={newRouter.name}
                  onChange={(e) => onNewRouterChange({ ...newRouter, name: e.target.value })}
                  placeholder="Router(x)"
                  className="w-full px-3 py-2 border rounded-lg"
                />
              </div>
              {/* IPv6 address */}
              <div>
                <label className="block text-sm font-medium mb-1">IPv6 Address</label>
                <input
                  type="text"
                  value={newRouter.address}
                  onChange={(e) => onNewRouterChange({ ...newRouter, address: e.target.value })}
                  placeholder="2001:db8::1"
                  className="w-full px-3 py-2 border rounded-lg"
                />
              </div>
              {/* Interface name */}
              <div>
                <label className="block text-sm font-medium mb-1">Interface (for sending RAs)</label>
                <input
                  type="text"
                  value={newRouter.interface}
                  onChange={(e) => onNewRouterChange({ ...newRouter, interface: e.target.value })}
                  placeholder="eth0"
                  className="w-full px-3 py-2 border rounded-lg"
                />
              </div>

              {/* Advanced interface configuration */}
              <div className="border-t pt-4 mt-4">
                <h4 className="font-semibold mb-3 text-sm">Interface Configuration</h4>
                <div className="grid grid-cols-2 gap-4">
                  {/* RA send interval */}
                  <div>
                    <label className="block text-xs font-medium mb-1">RA Interval (ms)</label>
                    <input
                      type="number"
                      value={newRouter.ra_interval_milliseconds}
                      onChange={(e) => onNewRouterChange({ ...newRouter, ra_interval_milliseconds: parseInt(e.target.value) || 0 })}
                      placeholder="3000"
                      className="w-full px-3 py-2 border rounded-lg text-sm"
                    />
                  </div>
                  {/* Current hop limit */}
                  <div>
                    <label className="block text-xs font-medium mb-1">Current Hop Limit</label>
                    <input
                      type="number"
                      value={newRouter.current_hop_limit}
                      onChange={(e) => onNewRouterChange({ ...newRouter, current_hop_limit: parseInt(e.target.value) || 0 })}
                      placeholder="64"
                      className="w-full px-3 py-2 border rounded-lg text-sm"
                    />
                  </div>
                  {/* Router lifetime */}
                  <div>
                    <label className="block text-xs font-medium mb-1">Router Lifetime (s)</label>
                    <input
                      type="number"
                      value={newRouter.router_lifetime_seconds}
                      onChange={(e) => onNewRouterChange({ ...newRouter, router_lifetime_seconds: parseInt(e.target.value) || 0 })}
                      placeholder="1800"
                      className="w-full px-3 py-2 border rounded-lg text-sm"
                    />
                  </div>
                  {/* Reachable time */}
                  <div>
                    <label className="block text-xs font-medium mb-1">Reachable Time (ms)</label>
                    <input
                      type="number"
                      value={newRouter.reachable_time_milliseconds}
                      onChange={(e) => onNewRouterChange({ ...newRouter, reachable_time_milliseconds: parseInt(e.target.value) || 0 })}
                      placeholder="0"
                      className="w-full px-3 py-2 border rounded-lg text-sm"
                    />
                  </div>
                  {/* Retransmit time */}
                  <div>
                    <label className="block text-xs font-medium mb-1">Retransmit Time (ms)</label>
                    <input
                      type="number"
                      value={newRouter.retransmit_time_milliseconds}
                      onChange={(e) => onNewRouterChange({ ...newRouter, retransmit_time_milliseconds: parseInt(e.target.value) || 0 })}
                      placeholder="0"
                      className="w-full px-3 py-2 border rounded-lg text-sm"
                    />
                  </div>
                </div>
                {/* M / O flag checkboxes */}
                <div className="mt-3 space-y-2">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={newRouter.managed}
                      onChange={(e) => onNewRouterChange({ ...newRouter, managed: e.target.checked })}
                      className="rounded"
                    />
                    <span className="text-sm">Managed Address Configuration (M flag)</span>
                  </label>
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={newRouter.other}
                      onChange={(e) => onNewRouterChange({ ...newRouter, other: e.target.checked })}
                      className="rounded"
                    />
                    <span className="text-sm">Other Configuration (O flag)</span>
                  </label>
                </div>
              </div>

              {/* Add router button */}
              <button
                onClick={onAddRouter}
                disabled={!newRouter.name || !newRouter.address || !newRouter.interface}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:bg-gray-400 disabled:cursor-not-allowed w-full justify-center"
              >
                <Plus className="w-4 h-4" />
                Add Router
              </button>
            </div>
          </div>
        </div>

        {/* Modal footer */}
        <div className="p-6 border-t flex justify-end gap-3">
          <button
            onClick={onClose}
            className="px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
