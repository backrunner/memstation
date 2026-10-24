import { workspaceEntryURL } from '#lib/scope/navigation.ts';
import type { GenericMemoryDocumentKind, MemoryDocumentKind } from '#lib/types.ts';

export type MemoryRouteKind =
  | 'summaries'
  | 'impressions'
  | 'facts'
  | 'keys'
  | 'items'
  | 'events'
  | 'tasks'
  | 'procedures'
  | 'artifacts'
  | 'entities'
  | 'agent-actions';

const genericRouteKinds: Record<string, GenericMemoryDocumentKind> = {
  events: 'event',
  tasks: 'task',
  procedures: 'procedure',
  artifacts: 'artifact',
  entities: 'entity',
  'agent-actions': 'agent_action'
};

function querySuffix(params: Record<string, string | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value?.trim()) {
      query.set(key, value.trim());
    }
  }
  const raw = query.toString();
  return raw ? `?${raw}` : '';
}

export function normalizeMemoryRouteKind(value: string): MemoryRouteKind | null {
  switch (value) {
    case 'summaries':
    case 'impressions':
    case 'facts':
    case 'keys':
    case 'items':
    case 'events':
    case 'tasks':
    case 'procedures':
    case 'artifacts':
    case 'entities':
    case 'agent-actions':
      return value;
    case 'agent_actions':
      return 'agent-actions';
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
  const documentKind = documentKindFromRoute(kind);
  if (documentKind && kind in genericRouteKinds) {
    return workspaceEntryURL(targetId, `/memory/items${querySuffix({ type: documentKind, user_id: userId })}`);
  }
  const suffix = querySuffix({ user_id: userId });
  return workspaceEntryURL(targetId, `/memory/${kind}${suffix}`);
}

export function memoryDetailHref(kind: MemoryRouteKind, id: string, userId?: string, targetId?: string): string {
  const suffix = querySuffix({ user_id: userId });
  return workspaceEntryURL(targetId, `/memory/${kind}/${encodeURIComponent(id)}${suffix}`);
}
