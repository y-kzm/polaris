import { useState } from 'react';
import { ChevronDown, ChevronRight, Trash2 } from 'lucide-react';
import type { Entry, ServiceEndpoints } from '../types';
import { errorMessage } from '../utils/api';

interface EntryRowProps {
  entry: Entry;
  fetchEndpoints: (serviceName: string) => Promise<ServiceEndpoints>;
  /** Shows a remove button (in a form). */
  onRemove?: () => void;
}

type Lookup =
  | { status: 'loading' }
  | { status: 'done'; prefixes: string[] }
  | { status: 'error'; message: string };

/**
 * One entry of a rule. A service entry names a service; the backend resolves
 * its prefixes at every apply, so the prefixes shown here are only what the
 * endpoint sources answer right now.
 */
export default function EntryRow({ entry, fetchEndpoints, onRemove }: EntryRowProps) {
  const [open, setOpen] = useState(false);
  const [lookup, setLookup] = useState<Lookup | null>(null);
  const isService = entry.type === 'service';

  const toggle = async () => {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    setLookup({ status: 'loading' });
    try {
      const endpoints = await fetchEndpoints(entry.value);
      setLookup({ status: 'done', prefixes: endpoints.ipv6_prefixes ?? [] });
    } catch (e) {
      setLookup({ status: 'error', message: errorMessage(e) });
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 flex-wrap min-w-0">
          <span className={`px-1.5 py-0.5 text-xs rounded ${
            isService ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'
          }`}>
            {isService ? 'service' : entry.type === 'ipv6' ? 'IPv6' : String(entry.type).toUpperCase()}
          </span>
          <span className={`text-xs text-gray-700 ${isService ? 'font-medium' : 'font-mono'}`}>{entry.value}</span>
          {!isService && entry.service && (
            <span className="text-xs text-gray-400">({entry.service})</span>
          )}
          {isService && (
            <button
              onClick={toggle}
              className="inline-flex items-center gap-0.5 text-xs text-blue-600 hover:text-blue-800"
            >
              {open ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
              current prefixes
            </button>
          )}
        </div>
        {onRemove && (
          <button onClick={onRemove} className="text-red-400 hover:text-red-600 flex-shrink-0" title="Remove entry">
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        )}
      </div>

      {isService && open && lookup && (
        <div className="mt-1 ml-4 pl-3 border-l-2 border-green-200 text-xs">
          {lookup.status === 'loading' && <span className="text-gray-400 italic">Loading...</span>}
          {lookup.status === 'error' && <span className="text-red-600 break-words">{lookup.message}</span>}
          {lookup.status === 'done' && (
            lookup.prefixes.length === 0 ? (
              <span className="text-gray-500">The service has no IPv6 prefix at the moment.</span>
            ) : (
              <>
                <p className="text-gray-500 mb-1">
                  {lookup.prefixes.length} IPv6 prefix{lookup.prefixes.length !== 1 ? 'es' : ''} right now;
                  resolved again at every apply.
                </p>
                <div className="flex flex-wrap gap-1 max-h-32 overflow-y-auto">
                  {lookup.prefixes.map(p => (
                    <span key={p} className="px-1.5 py-0.5 bg-gray-100 text-gray-700 rounded font-mono">{p}</span>
                  ))}
                </div>
              </>
            )
          )}
        </div>
      )}
    </div>
  );
}
