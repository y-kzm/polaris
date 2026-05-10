export type { NeighborProvider, ServiceProvider } from './types';
export { RestNeighborProvider } from './neighbor-rest';
export { RestServiceProvider } from './service-rest';

import type { NeighborProvider, ServiceProvider } from './types';
import { RestNeighborProvider } from './neighbor-rest';
import { RestServiceProvider } from './service-rest';

export function createNeighborProvider(): NeighborProvider {
  return new RestNeighborProvider();
}

export function createServiceProvider(): ServiceProvider {
  return new RestServiceProvider();
}
