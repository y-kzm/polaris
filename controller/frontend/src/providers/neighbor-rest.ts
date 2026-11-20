import type { Neighbor } from '../types';
import type { NeighborProvider } from './types';
import { api } from '../utils/api';

export class RestNeighborProvider implements NeighborProvider {
  async fetchNeighbors(): Promise<Neighbor[]> {
    return (await api<Neighbor[] | null>('/api/neighbors')) ?? [];
  }

  async refreshNeighbors(): Promise<Neighbor[]> {
    return (await api<Neighbor[] | null>('/api/neighbors/refresh', { method: 'POST' })) ?? [];
  }
}
