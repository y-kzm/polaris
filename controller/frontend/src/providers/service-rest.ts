import type { FqdnService, ServiceEndpoints } from '../types';
import type { ServiceProvider } from './types';
import { getApiUrl } from '../utils/api';

export class RestServiceProvider implements ServiceProvider {
  private readonly baseUrl: string;

  constructor(baseUrl = getApiUrl()) {
    this.baseUrl = baseUrl;
  }

  async fetchServices(): Promise<FqdnService[]> {
    const response = await fetch(`${this.baseUrl}/api/fqdn/services`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    const data = await response.json();

    if (Array.isArray(data)) return data;
    if (data.services && Array.isArray(data.services)) {
      return data.services.map((name: string) => ({
        service: name,
        name,
        instance: data.instance,
        version: data.version,
      }));
    }
    return [];
  }

  async fetchEndpoints(service: FqdnService): Promise<ServiceEndpoints | null> {
    const serviceName = service.service || service.name;
    if (!serviceName) return null;

    const response = await fetch(
      `${this.baseUrl}/api/fqdn/services/${encodeURIComponent(serviceName)}/endpoints`
    );
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    const data = await response.json();

    // Normalise: API may return a raw IPv6 prefix array or a ServiceEndpoints object
    if (Array.isArray(data)) return { ipv6_prefixes: data };
    return data as ServiceEndpoints;
  }
}
