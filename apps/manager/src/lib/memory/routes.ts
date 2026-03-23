export type MemoryRouteKind = 'summaries' | 'impressions' | 'facts' | 'keys';
export type MemoryDocumentKind = 'summary' | 'impression' | 'fact';

export function normalizeMemoryRouteKind(value: string): MemoryRouteKind | null {
  switch (value) {
    case 'summaries':
    case 'impressions':
    case 'facts':
    case 'keys':
      return value;
    default:
      return null;
  }
}

export function documentKindFromRoute(kind: MemoryRouteKind): MemoryDocumentKind | null {
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
    default:
      return null;
  }
}

export function memoryCollectionHref(kind: MemoryRouteKind, userId?: string): string {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return `/memory/${kind}${suffix}`;
}

export function memoryDetailHref(kind: MemoryRouteKind, id: string, userId?: string): string {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return `/memory/${kind}/${encodeURIComponent(id)}${suffix}`;
}
