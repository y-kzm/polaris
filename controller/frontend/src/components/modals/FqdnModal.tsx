import { useEffect, useState } from 'react';
import type { FqdnService, ServiceCatalog, ServiceEndpoints } from '../../types';
import { errorMessage } from '../../utils/api';
import SourceBanner from '../SourceBanner';

interface FqdnModalProps {
  catalog: ServiceCatalog;
  /** Called with the name of the chosen service. */
  onSelectService: (serviceName: string) => void;
  onClose: () => void;
}

type Preview =
  | { status: 'loading' }
  | { status: 'done'; endpoints: ServiceEndpoints }
  | { status: 'error'; message: string };

const serviceKey = (service: FqdnService) => service.name || service.service || '';

/**
 * Service picker. Choosing a service adds ONE rule entry that names it; the
 * backend looks its prefixes up at every apply. The endpoints shown here are
 * a preview only.
 */
export default function FqdnModal({ catalog, onSelectService, onClose }: FqdnModalProps) {
  const [previews, setPreviews] = useState<Record<string, Preview>>({});
  const { reload, fetchEndpoints } = catalog;

  // The list is read again every time the picker opens.
  useEffect(() => { reload(); }, [reload]);

  const loadPreview = async (key: string) => {
    if (!key || previews[key]) return;
    setPreviews(prev => ({ ...prev, [key]: { status: 'loading' } }));
    try {
      const endpoints = await fetchEndpoints(key);
      setPreviews(prev => ({ ...prev, [key]: { status: 'done', endpoints } }));
    } catch (e) {
      setPreviews(prev => ({ ...prev, [key]: { status: 'error', message: errorMessage(e) } }));
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-xl font-bold">Select Service</h2>
          <p className="text-sm text-gray-600 mt-1">
            The rule refers to the service by name. Its IPv6 prefixes are looked up by the
            controller at every apply, so the rule follows the service when its prefixes change.
          </p>
        </div>

        {/* Service list */}
        <div className="overflow-y-auto max-h-[60vh] p-6">
          <SourceBanner kind="endpoints" sources={catalog.sources} />
          {catalog.error && (
            <p className="mb-4 text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
              The services could not be listed: {catalog.error}
            </p>
          )}
          {catalog.services.length === 0 ? (
            !catalog.error && (
              <div className="text-center py-12">
                <p className="text-gray-500 font-medium">No services available</p>
                <p className="text-sm text-gray-400 mt-1">No endpoint source is configured, or the sources know no service.</p>
              </div>
            )
          ) : (
            <div className="space-y-3">
              {catalog.services.map(service => {
                const key = serviceKey(service);
                const preview = previews[key];
                const endpoints = preview?.status === 'done' ? preview.endpoints : undefined;
                const fqdns = endpoints?.fqdns ?? [];

                return (
                  <div
                    key={key}
                    className="border rounded-lg p-4 hover:bg-gray-50 cursor-pointer transition"
                    onClick={() => onSelectService(key)}
                    onMouseEnter={() => loadPreview(key)}
                  >
                    <div className="flex items-start justify-between">
                      <div className="flex-1 min-w-0">
                        <div className="font-semibold text-lg mb-2">{key}</div>

                        {service.description && (
                          <div className="text-sm text-gray-600 mb-2">{service.description}</div>
                        )}

                        {!preview && (
                          <div className="text-xs text-gray-400 mb-2 italic">Hover to preview its endpoints...</div>
                        )}
                        {preview?.status === 'loading' && (
                          <div className="text-xs text-gray-400 mb-2 italic">Loading...</div>
                        )}
                        {preview?.status === 'error' && (
                          <div className="text-xs text-red-600 mb-2 break-words">{preview.message}</div>
                        )}

                        {fqdns.length > 0 && (
                          <div className="mb-2">
                            <div className="text-xs font-medium text-gray-700 mb-1">FQDNs:</div>
                            <div className="flex flex-wrap gap-1">
                              {fqdns.slice(0, 8).map(name => (
                                <span key={name} className="px-2 py-0.5 bg-blue-100 text-blue-700 text-xs rounded font-mono">
                                  {name}
                                </span>
                              ))}
                              {fqdns.length > 8 && (
                                <span className="px-2 py-0.5 bg-gray-100 text-gray-600 text-xs rounded">
                                  +{fqdns.length - 8} more
                                </span>
                              )}
                            </div>
                          </div>
                        )}

                        {endpoints && (
                          <div className="text-xs text-gray-600">
                            {(endpoints.ipv6_prefixes ?? []).length} IPv6 prefix
                            {(endpoints.ipv6_prefixes ?? []).length !== 1 ? 'es' : ''} right now
                          </div>
                        )}
                      </div>
                      <button className="ml-4 px-3 py-1 bg-blue-600 text-white rounded hover:bg-blue-700 text-sm flex-shrink-0">
                        Select
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Modal footer */}
        <div className="p-6 border-t flex justify-end gap-3">
          <button onClick={onClose} className="px-4 py-2 border rounded-lg hover:bg-gray-50">
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
