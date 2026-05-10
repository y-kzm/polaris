import type { Neighbor, FqdnService, ServiceEndpoints } from '../types';

export interface NeighborProvider {
  fetchNeighbors(): Promise<Neighbor[]>;
}

export interface ServiceProvider {
  fetchServices(): Promise<FqdnService[]>;
  fetchEndpoints(service: FqdnService): Promise<ServiceEndpoints | null>;
}
