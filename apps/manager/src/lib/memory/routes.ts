import { workspaceLink } from '#lib/scope/navigation.ts';
import type { GenericMemoryDocumentKind, MemoryDocumentKind } from '#lib/types.ts';

export type MemoryRouteKind =
  | 'summaries'
  | 'impressions'
  | 'facts'
  | 'keys'
  | 'items'
  | 'episodes'
  | 'events'
  | 'tasks'
  | 'procedures'
  | 'artifacts'
  | 'entities'
  | 'agent-actions';

const genericRouteKinds: Record<string, GenericMemoryDocumentKind> = {
  episodes: 'episode',
  events: 'event',
  tasks: 'task',
  procedures: 'procedure',
  artifacts: 'artifact',
  entities: 'entity',
  'agent-actions': 'agent_action'
};

export function normalizeMemoryRouteKind(value: string): MemoryRouteKind | null {
  switch (value) {
    case 'summaries':
    case 'impressions':
    case 'facts':
    case 'keys':
    case 'items':
    case 'episodes':
    case 'events':
    case 'tasks':
    case 'procedures':
    case 'artifacts':
    case 'entities':
    case 'agent-actions':
      return value;
    default:
      return null;
  }
}

export function documentKindFromRoute(kind: MemoryRouteKind): MemoryDocumentKind | null {
  if (kind in genericRouteKinds) {
    return genericRouteKinds[kind];
  }
  switch (kind) {
    case 'summaries':
      return 'summary';
    case 'impressions':
      return 'impression';
    case 'facts':
      return 'fact';
    default:
      return null;
  }
}

export function memoryRouteKindFromDocumentType(value: string): MemoryRouteKind | null {
  switch (value) {
    case 'summary':
      return 'summaries';
    case 'impression':
      return 'impressions';
    case 'fact':
      return 'facts';
    case 'key_memory':
      return 'keys';
    case 'episode':
      return 'episodes';
    case 'event':
      return 'events';
    case 'task':
      return 'tasks';
    case 'procedure':
      return 'procedures';
    case 'artifact':
      return 'artifacts';
    case 'entity':
      return 'entities';
    case 'agent_action':
      return 'agent-actions';
    default:
      return null;
  }
}

export function memoryCollectionHref(kind: MemoryRouteKind, userId?: string, targetId?: string): string {
  return workspaceLink(targetId, kind === 'keys' ? 'keys' : 'memory', {owner:userId,kind:documentKindFromRoute(kind) || undefined});
}
export function memoryDetailHref(kind: MemoryRouteKind, id: string, userId?: string, targetId?: string): string {
  return workspaceLink(targetId,kind === 'keys' ? 'keys' : 'memory', {owner:userId,record:id,kind:documentKindFromRoute(kind) || undefined});
}
