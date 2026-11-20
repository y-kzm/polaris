import { useState } from 'react';
import { AlertTriangle, ChevronDown, ChevronRight } from 'lucide-react';
import type { PlannedMessage, RouterResult } from '../types';
import { uniqueLines } from '../utils/format';
import StatusLed from './StatusLed';

/** All warnings of a plan or of an apply, each one once. */
export function WarningList({ warnings }: { warnings: string[] | null | undefined }) {
  const lines = uniqueLines(warnings);
  if (lines.length === 0) return null;
  return (
    <div className="p-3 rounded-lg border border-yellow-300 bg-yellow-50">
      <p className="flex items-center gap-1.5 text-xs font-semibold text-yellow-800 mb-1.5">
        <AlertTriangle className="w-3.5 h-3.5 text-yellow-600" />
        Warnings ({lines.length})
      </p>
      <ul className="list-disc pl-5 space-y-1 text-xs text-yellow-800">
        {lines.map(line => <li key={line} className="break-words">{line}</li>)}
      </ul>
    </div>
  );
}

function ClientList({ clients }: { clients: string[] }) {
  const [open, setOpen] = useState(false);
  if (clients.length === 0) return <span className="text-gray-400">no clients</span>;
  return (
    <span>
      <button
        onClick={() => setOpen(!open)}
        className="inline-flex items-center gap-0.5 text-blue-600 hover:text-blue-800 font-medium"
      >
        {open ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
        {clients.length} client{clients.length !== 1 ? 's' : ''}
      </button>
      {open && (
        <span className="mt-1 flex flex-wrap gap-1">
          {clients.map(c => (
            <span key={c} className="px-1.5 py-0.5 bg-gray-100 text-gray-700 rounded font-mono">{c}</span>
          ))}
        </span>
      )}
    </span>
  );
}

function MessageRow({ message }: { message: PlannedMessage }) {
  const routes = message.routes ?? [];
  const isDefaultRouter = message.preference === 'high';
  return (
    <div className="text-xs bg-gray-50 rounded px-3 py-2 space-y-1">
      <div className="flex flex-wrap gap-x-4 gap-y-0.5 text-gray-600">
        <span>
          Source:{' '}
          <span className="font-mono text-gray-800">{message.source_address || '(baseline address)'}</span>
        </span>
        <span>Preference: <span className="font-medium text-gray-800">{message.preference}</span></span>
        <span>
          Router Lifetime:{' '}
          <span className="font-medium text-gray-800">{message.router_lifetime_seconds}s</span>
        </span>
      </div>
      <div className="text-gray-600">
        Routes:{' '}
        {isDefaultRouter ? (
          <span className="px-1.5 py-0.5 rounded font-medium bg-purple-100 text-purple-700">default router</span>
        ) : routes.length === 0 ? (
          <span className="text-gray-400">none</span>
        ) : (
          <span className="inline-flex flex-wrap gap-1 align-top">
            {routes.map(r => (
              <span key={r} className="px-1.5 py-0.5 bg-blue-100 text-blue-700 rounded font-mono">{r}</span>
            ))}
          </span>
        )}
      </div>
      <div className="text-gray-600">Clients: <ClientList clients={message.clients ?? []} /></div>
    </div>
  );
}

interface RouterResultListProps {
  routers: RouterResult[];
  /** plan: what an apply would do. result: what an apply did. */
  mode: 'plan' | 'result';
  /** Show the control request messages (present only in a plan). */
  showMessages?: boolean;
}

/** The per-router part of a plan or of an apply result. */
export function RouterResultList({ routers, mode, showMessages = false }: RouterResultListProps) {
  if (routers.length === 0) {
    return <p className="text-sm text-gray-400 text-center py-4">No routers are registered.</p>;
  }
  return (
    <div className="space-y-3">
      {routers.map(r => {
        const failed = mode === 'result' && !r.success;
        const messages = r.messages ?? [];
        return (
          <div
            key={`${r.router}|${r.address}`}
            className={`border rounded-lg p-3 ${failed ? 'border-red-300 bg-red-50' : ''}`}
          >
            <div className="flex items-center justify-between gap-2 flex-wrap">
              <div className="flex items-center gap-2 flex-wrap min-w-0">
                <span className="font-semibold text-sm text-gray-800">{r.router}</span>
                <span className="font-mono text-xs text-gray-500">{r.address}</span>
                <span className={`px-1.5 py-0.5 text-xs rounded font-medium ${
                  r.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-gray-200 text-gray-600'
                }`}>
                  {r.status === 'active' ? 'active' : 'inactive: excluded from the policy'}
                </span>
                {mode === 'result' && (
                  <span className={`px-1.5 py-0.5 text-xs rounded font-medium ${
                    r.success ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'
                  }`}>
                    {r.success ? 'applied' : 'failed'}
                  </span>
                )}
              </div>
              <StatusLed
                status={r.reachable ? 'up' : 'down'}
                label={r.reachable ? 'Reachable' : 'Unreachable'}
              />
            </div>

            <div className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-xs font-mono">
              <span className="text-green-700" title="RA configurations added">+{r.added} added</span>
              <span className="text-yellow-700" title="RA configurations updated">~{r.updated} updated</span>
              <span className="text-red-700" title="RA configurations deleted">−{r.deleted} deleted</span>
              <span className="text-gray-500" title="RA configurations left as they are">={r.unchanged} unchanged</span>
            </div>
            <p className="mt-1 text-xs text-gray-600">
              {mode === 'plan'
                ? `${r.withdrawn} host(s) will get a withdrawal RA`
                : `${r.withdrawn} host(s) were sent a withdrawal RA`}
            </p>

            {r.error && (
              <p className="mt-2 text-xs text-red-700 bg-red-50 border border-red-200 rounded px-2 py-1.5 break-words">
                {r.error}
              </p>
            )}
            {r.note && (
              <p className="mt-2 text-xs text-yellow-800 bg-yellow-50 border border-yellow-200 rounded px-2 py-1.5 break-words">
                {r.note}
              </p>
            )}

            {showMessages && (
              <div className="mt-3 pt-3 border-t">
                <p className="text-xs font-semibold text-gray-600 mb-1.5">
                  Control request messages ({messages.length})
                </p>
                {messages.length === 0 ? (
                  <p className="text-xs text-gray-400">This router sends no policy RA.</p>
                ) : (
                  <div className="space-y-1.5">
                    {messages.map((m, i) => <MessageRow key={i} message={m} />)}
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
