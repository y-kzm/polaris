import { useState } from 'react';
import type { FqdnService, ServiceEndpoints } from '../../types';

interface FqdnModalProps {
  fqdnServices: FqdnService[];
  serviceEndpoints: Record<string, ServiceEndpoints>;
  onSelectService: (service: FqdnService) => void;
  onFetchEndpoints: (service: FqdnService) => void;
  onClose: () => void;
}

export default function FqdnModal({
  fqdnServices,
  serviceEndpoints,
  onSelectService,
  onFetchEndpoints,
  onClose,
}: FqdnModalProps) {
  const [loadingServices, setLoadingServices] = useState<Set<string>>(new Set());

  const handleMouseEnter = async (service: FqdnService) => {
    const key = service.service || service.name;
    if (!key || serviceEndpoints[key] !== undefined) return;
    setLoadingServices(prev => new Set(prev).add(key));
    await onFetchEndpoints(service);
    setLoadingServices(prev => { const next = new Set(prev); next.delete(key); return next; });
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-xl font-bold">Select FQDN Service</h2>
          <p className="text-sm text-gray-600 mt-1">
            Choose a service to add its IPv6 prefixes
          </p>
        </div>

        {/* Service list */}
        <div className="overflow-y-auto max-h-[60vh] p-6">
          {fqdnServices.length === 0 ? (
            <div className="text-center py-12">
              <div className="text-gray-400 mb-2">
                <svg className="mx-auto h-12 w-12" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4"
                  />
                </svg>
              </div>
              <p className="text-gray-500 font-medium">No FQDN services available</p>
              <p className="text-sm text-gray-400 mt-1">The FQDN API service may be unavailable</p>
            </div>
          ) : (
            <div className="space-y-3">
              {fqdnServices.map((service, idx) => {
                const serviceKey = service.service || service.name;
                const displayName = service.name || service.service;
                const endpoints = serviceEndpoints[serviceKey ?? ''];

                return (
                  <div
                    key={idx}
                    className="border rounded-lg p-4 hover:bg-gray-50 cursor-pointer transition"
                    onClick={() => onSelectService(service)}
                    onMouseEnter={() => handleMouseEnter(service)}
                  >
                    <div className="flex items-start justify-between">
                      <div className="flex-1">
                        {/* Service name */}
                        <div className="font-semibold text-lg mb-2">{displayName}</div>

                        {/* FQDN list preview */}
                        {endpoints?.fqdns && endpoints.fqdns.length > 0 ? (
                          <div className="mb-2">
                            <div className="text-xs font-medium text-gray-700 mb-1">FQDNs:</div>
                            <div className="flex flex-wrap gap-1">
                              {endpoints.fqdns.slice(0, 8).map((fqdn, i) => (
                                <span key={i} className="px-2 py-0.5 bg-blue-100 text-blue-700 text-xs rounded font-mono">
                                  {fqdn}
                                </span>
                              ))}
                              {endpoints.fqdns.length > 8 && (
                                <span className="px-2 py-0.5 bg-gray-100 text-gray-600 text-xs rounded">
                                  +{endpoints.fqdns.length - 8} more
                                </span>
                              )}
                            </div>
                          </div>
                        ) : loadingServices.has(serviceKey ?? '') ? (
                          <div className="text-xs text-gray-400 mb-2 italic">Loading...</div>
                        ) : (
                          <div className="text-xs text-gray-400 mb-2 italic">
                            Hover to load FQDNs...
                          </div>
                        )}

                        {/* IPv6 prefix count */}
                        {endpoints?.ipv6_prefixes && (
                          <div className="text-xs text-gray-600">
                            {endpoints.ipv6_prefixes.length} IPv6 prefix{endpoints.ipv6_prefixes.length !== 1 ? 'es' : ''}
                          </div>
                        )}

                        {service.description && (
                          <div className="text-sm text-gray-600 mb-2">
                            {service.description}
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
          <button
            onClick={onClose}
            className="px-4 py-2 border rounded-lg hover:bg-gray-50"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
