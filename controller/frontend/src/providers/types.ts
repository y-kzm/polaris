import type { Neighbor, FqdnService, ServiceEndpoints } from '../types';

export interface NeighborProvider {
  fetchNeighbors(): Promise<Neighbor[]>;
  /** Asks the sources to refresh now and returns the new list. */
  refreshNeighbors(): Promise<Neighbor[]>;
}

export interface ServiceProvider {
  fetchServices(): Promise<FqdnService[]>;
  /** Rejects with the backend's message (404 unknown service, 502 no source answered). */
  fetchEndpoints(serviceName: string): Promise<ServiceEndpoints>;
}
