import type { FqdnService, ServiceEndpoints } from '../types';
import type { ServiceProvider } from './types';
import { api } from '../utils/api';

export class RestServiceProvider implements ServiceProvider {
  async fetchServices(): Promise<FqdnService[]> {
    return (await api<FqdnService[] | null>('/api/fqdn/services')) ?? [];
  }

  async fetchEndpoints(serviceName: string): Promise<ServiceEndpoints> {
    const data = await api<ServiceEndpoints | null>(
      `/api/fqdn/services/${encodeURIComponent(serviceName)}/endpoints`,
    );
    return data ?? {};
  }
}
