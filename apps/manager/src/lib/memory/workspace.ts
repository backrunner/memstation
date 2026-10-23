import {
  fetchAdminMemoryDocuments,
  searchAdminMemories
} from '$lib/api/client';
import type {
  FactRecord,
  GenericMemoryDocumentKind,
  MemStationTarget,
  MemoryDocument,
  MemorySummaryRecord,
  SearchMemoriesResult
} from '$lib/types';

export interface MemoryWorkspaceData {
  memorySummaries: MemorySummaryRecord[];
  facts: FactRecord[];
  genericItems: MemoryDocument[];
}

export const genericMemoryKinds: GenericMemoryDocumentKind[] = ['event', 'task', 'procedure', 'artifact', 'entity', 'agent_action'];

function toSummaryRecord(item: MemoryDocument): MemorySummaryRecord {
  return {
    id: item.id,
    summary: item.content,
    topicLabel: typeof item.metadata?.topic_label === 'string' ? item.metadata.topic_label : null
  };
}

function toFactRecord(item: MemoryDocument): FactRecord {
  return {
    id: item.id,
    content: item.content
  };
}

export async function fetchMemoryWorkspace(target: MemStationTarget, userId?: string): Promise<MemoryWorkspaceData> {
  const [summaries, facts, genericItems] = await Promise.all([
    fetchAdminMemoryDocuments(target, { kind: 'summary', userId, limit: 2 }),
    fetchAdminMemoryDocuments(target, { kind: 'fact', userId, limit: 1 }),
    fetchAdminMemoryDocuments(target, { kind: genericMemoryKinds, userId, limit: 1 }),
  ]);

  return {
    memorySummaries: summaries.map(toSummaryRecord),
    facts: facts.map(toFactRecord),
    genericItems
  };
}

export function searchMemories(
  target: MemStationTarget,
  query: string,
  topN = 10,
  userId?: string,
  options?: { dateFrom?: string; dateTo?: string; timeTiers?: string[]; asOf?: string; knownAt?: string; relationMode?: string; entityScope?: string }
): Promise<SearchMemoriesResult> {
  return searchAdminMemories(target, {
    query,
    topN,
    userId,
    asOf: options?.asOf,
    knownAt: options?.knownAt,
    relationMode: options?.relationMode,
    entityScope: options?.entityScope,
    dateFrom: options?.dateFrom,
    dateTo: options?.dateTo,
    timeTiers: options?.timeTiers
  });
}
