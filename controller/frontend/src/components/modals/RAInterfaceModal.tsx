import { X } from 'lucide-react';
import type { RAInterfaceConfig } from '../../types';

interface RAInterfaceModalProps {
  routerName: string;
  interfaces: RAInterfaceConfig[];
  onClose: () => void;
}

function LifetimeValue(v: { value: number } | null | undefined, fallback = '∞') {
  if (v == null) return fallback;
  return `${v.value}s`;
}

function Badge({ label, variant }: { label: string; variant: 'blue' | 'green' | 'yellow' | 'gray' | 'purple' }) {
  const cls: Record<string, string> = {
    blue:   'bg-blue-100 text-blue-700',
    green:  'bg-green-100 text-green-700',
    yellow: 'bg-yellow-100 text-yellow-700',
    gray:   'bg-gray-100 text-gray-600',
    purple: 'bg-purple-100 text-purple-700',
  };
  return (
    <span className={`px-1.5 py-0.5 text-xs rounded font-medium ${cls[variant]}`}>
      {label}
    </span>
  );
}

function prefBadge(pref: string) {
  if (pref === 'high')   return <Badge label="high"   variant="blue" />;
  if (pref === 'low')    return <Badge label="low"    variant="yellow" />;
  return                        <Badge label="medium" variant="gray" />;
}

export default function RAInterfaceModal({ routerName, interfaces, onClose }: RAInterfaceModalProps) {
  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-xl shadow-xl w-full max-w-2xl max-h-[85vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b">
          <div>
            <h2 className="font-semibold text-gray-800">RA Interface Details</h2>
            <p className="text-xs text-gray-500 mt-0.5">{routerName}</p>
          </div>
          <button onClick={onClose} className="p-1 rounded hover:bg-gray-100 cursor-pointer">
            <X className="w-4 h-4 text-gray-500" />
          </button>
        </div>

        {/* Body */}
        <div className="overflow-y-auto flex-1 px-5 py-4 space-y-5">
          {interfaces.length === 0 && (
            <p className="text-sm text-gray-400 text-center py-8">No interface configs returned.</p>
          )}
          {interfaces.map((iface) => (
            <div key={iface.id} className="border rounded-lg overflow-hidden">
              {/* Config header */}
              <div className="flex items-center justify-between bg-gray-50 px-4 py-2.5 border-b">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-sm font-semibold text-gray-700">{iface.name}</span>
                  <Badge label={`config #${iface.id}`} variant={iface.id === 0 ? 'gray' : 'purple'} />
                  {iface.clients?.length > 0 && <Badge label="unicast" variant="blue" />}
                </div>
                <div className="flex items-center gap-1.5">
                  {prefBadge(iface.preference)}
                </div>
              </div>

              <div className="px-4 py-3 space-y-3 text-sm">
                {/* Core parameters */}
                <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
                  <Row label="RA Interval"       value={`${iface.ra_interval_milliseconds} ms`} />
                  <Row label="Router Lifetime"   value={`${iface.router_lifetime_seconds} s`} />
                  <Row label="Hop Limit"         value={String(iface.current_hop_limit)} />
                  <Row label="Reachable Time"    value={`${iface.reachable_time_milliseconds} ms`} />
                  <Row label="Retransmit Time"   value={`${iface.retransmit_time_milliseconds} ms`} />
                  <Row label="Disable RS Reply"  value={iface.disable_rs_reply ? 'yes' : 'no'} highlight={iface.disable_rs_reply} />
                  <Row label="Managed (M)"       value={iface.managed ? 'yes' : 'no'} />
                  <Row label="Other (O)"         value={iface.other ? 'yes' : 'no'} />
                </div>

                {/* Prefixes */}
                {iface.prefixes?.length > 0 && (
                  <Section title="Prefixes">
                    {iface.prefixes.map((p, i) => (
                      <div key={i} className="font-mono text-xs bg-gray-50 px-3 py-2 rounded">
                        <div className="flex items-center justify-between">
                          <span className="text-blue-700 font-semibold">{p.prefix}</span>
                          <div className="flex gap-1.5">
                            {p.on_link   && <Badge label="on-link"   variant="green" />}
                            {p.autonomous && <Badge label="SLAAC"    variant="green" />}
                          </div>
                        </div>
                        <div className="mt-1 text-gray-500 space-x-4">
                          <span>valid {LifetimeValue(p.valid_lifetime_seconds)}</span>
                          <span>preferred {LifetimeValue(p.preferred_lifetime_seconds)}</span>
                        </div>
                      </div>
                    ))}
                  </Section>
                )}

                {/* Routes (RIO) */}
                {iface.routes?.length > 0 && (
                  <Section title="Route Information Options (RIO)">
                    {iface.routes.map((r, i) => (
                      <div key={i} className="font-mono text-xs bg-gray-50 px-3 py-2 rounded flex items-center justify-between">
                        <span className="text-purple-700 font-semibold">{r.prefix}</span>
                        <div className="flex items-center gap-2 text-gray-500">
                          {prefBadge(r.preference)}
                          <span>{r.lifetime_seconds}s</span>
                        </div>
                      </div>
                    ))}
                  </Section>
                )}

                {/* Unicast clients */}
                {iface.clients?.length > 0 && (
                  <Section title={`Unicast Clients (${iface.clients.length})`}>
                    <div className="space-y-1">
                      {iface.clients.map((c, i) => (
                        <div key={i} className="font-mono text-xs bg-blue-50 text-blue-800 px-3 py-1.5 rounded">
                          {c}
                        </div>
                      ))}
                    </div>
                  </Section>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function Row({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <>
      <span className="text-gray-500">{label}</span>
      <span className={`font-mono ${highlight ? 'text-orange-600 font-semibold' : 'text-gray-800'}`}>{value}</span>
    </>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-1.5">{title}</p>
      <div className="space-y-1">{children}</div>
    </div>
  );
}
