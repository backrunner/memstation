import type { MemStationTarget } from '#lib/types.ts';

let catalog: MemStationTarget[] = [];
export function configureTargetCatalog(targets: MemStationTarget[]): void { catalog = targets; }
export function registeredTarget(input: Pick<MemStationTarget, 'baseUrl'> & { id?: string }): MemStationTarget {
  const url = input.baseUrl.trim().replace(/\/+$/, '');
  const target = catalog.find((item) => item.baseUrl === url && (!input.id || item.id === input.id));
  if (!target) throw new Error('This endpoint is not enabled in this Manager. Select an available service.');
  return target;
}
export function managerTargetURL(target: Pick<MemStationTarget, 'baseUrl'> & { id?: string }, path: string): string {
  return `/manager/api/targets/${encodeURIComponent(registeredTarget(target).id)}${path}`;
}
