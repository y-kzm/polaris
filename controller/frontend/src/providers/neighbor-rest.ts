import type { Neighbor } from '../types';
import type { NeighborProvider } from './types';
import { getApiUrl } from '../utils/api';

export class RestNeighborProvider implements NeighborProvider {
  private readonly baseUrl: string;

  constructor(baseUrl = getApiUrl()) {
    this.baseUrl = baseUrl;
  }

  async fetchNeighbors(): Promise<Neighbor[]> {
    const response = await fetch(`${this.baseUrl}/api/neighbors`);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return response.json();
  }
}
