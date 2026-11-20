import { Router } from 'lucide-react';
import type { RouterConfig, RouterStatus } from '../types';
import StatusLed from './StatusLed';
import { INACTIVE_EXPLANATION } from '../utils/format';

interface RouterGridProps {
  routers: RouterConfig[];
  routerStatuses: Record<string, RouterStatus>;
  onToggleStatus: (id: string) => void;
  onInterfaceClick: (routerId: string, routerName: string) => void;
}

export default function RouterGrid({ routers, routerStatuses, onToggleStatus, onInterfaceClick }: RouterGridProps) {
  if (routers.length === 0) {
    return (
      <p className="text-sm text-gray-400 text-center py-4">
        No routers configured. Use <span className="font-medium">Routers</span> to add one.
      </p>
    );
  }

  return (
    <div
      className="grid gap-4"
      style={{ gridTemplateColumns: `repeat(${Math.min(routers.length, 3)}, 1fr)` }}
    >
      {routers.map(router => {
        const status = routerStatuses[router.id];
        const isActive = router.status === 'active';

        return (
          <div key={router.id} className="border rounded-lg p-4">
            <div className="flex items-center justify-between mb-2">
              <div className="flex items-center gap-2">
                <Router className="w-4 h-4 text-gray-500" />
                <span className="font-semibold text-sm">{router.name}</span>
              </div>
              <div className="flex items-center gap-2">
                {/* gRPC reachability */}
                {status && (
                  <StatusLed
                    status={status.reachable ? 'up' : 'down'}
                    label={status.reachable ? 'Connected' : 'Unreachable'}
                  />
                )}
                {/* Admin state toggle */}
                <button
                  onClick={() => onToggleStatus(router.id)}
                  title={isActive
                    ? `Active: takes part in the policy. Click to set inactive. ${INACTIVE_EXPLANATION}.`
                    : `${INACTIVE_EXPLANATION}. Click to set active.`}
                  className={`px-2 py-0.5 text-xs rounded font-medium cursor-pointer transition ${
                    isActive
                      ? 'bg-green-100 text-green-700 hover:bg-green-200'
                      : 'bg-gray-200 text-gray-700 hover:bg-gray-300'
                  }`}
                >
                  {isActive ? 'Active' : 'Inactive'}
                </button>
              </div>
            </div>

            <p className="text-xs font-mono text-gray-600">{router.address}</p>
            <p className="text-xs text-gray-400 mt-0.5">Interface: {router.interface}</p>
            {!isActive && (
              <p className="text-xs text-gray-600 bg-gray-100 rounded px-2 py-1 mt-2">{INACTIVE_EXPLANATION}.</p>
            )}

            {/* gRPC status detail */}
            {status && (
              <div className="mt-3 pt-3 border-t">
                {status.reachable ? (
                  <div className="space-y-1">
                    <p className="text-xs font-semibold text-gray-600 mb-1">
                      RA Interfaces ({status.interfaces.length})
                    </p>
                    {status.interfaces.map((iface, idx) => {
                      const ifaceStatus =
                        iface.state === 'Running' ? 'up'
                        : iface.state === 'Failing' ? 'down'
                        : 'warning';
                      return (
                        <button
                          key={idx}
                          onClick={() => onInterfaceClick(router.id, router.name)}
                          className="w-full text-xs bg-gray-50 hover:bg-blue-50 hover:border-blue-200 border border-transparent px-2 py-1.5 rounded flex items-center justify-between transition cursor-pointer"
                          title="Click to view RA parameters"
                        >
                          <div className="flex items-center gap-2">
                            <StatusLed status={ifaceStatus} />
                            <span className="font-mono">{iface.name}</span>
                          </div>
                          <span className="text-gray-400">
                            TX {iface.tx_solicited_ra + iface.tx_unsolicited_ra} RA
                          </span>
                        </button>
                      );
                    })}
                  </div>
                ) : (
                  <div className="flex items-center gap-1.5 text-xs text-red-600">
                    <StatusLed status="down" />
                    {status.error || 'Cannot connect to router'}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
