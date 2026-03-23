export function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => (typeof item === 'string' ? item.trim() : ''))
    .filter((item) => item.length > 0);
}

export function matchedKeywordsFromMetadata(metadata?: Record<string, unknown> | null): string[] {
  return stringList(metadata?.matched_keywords);
}

export function sourceKeywordsFromMetadata(metadata?: Record<string, unknown> | null): string[] {
  return stringList(metadata?.keywords);
}

export function recordList(value: unknown): Array<Record<string, unknown>> {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is Record<string, unknown> => typeof item === 'object' && item !== null);
}
