import {
  fetchAdminKeyMemories,
  fetchAdminMemoryDocuments,
  searchAdminMemories
} from '$lib/api/client';
import type {
  FactRecord,
  ImpressionRecord,
  KeyMemoryRecord,
  MemStationTarget,
  MemoryDocument,
  MemorySummaryRecord,
  SearchMemoriesResult
} from '$lib/types';

export interface MemoryWorkspaceData {
  memorySummaries: MemorySummaryRecord[];
  impressions: ImpressionRecord[];
  facts: FactRecord[];
  keyMemories: KeyMemoryRecord[];
}

function toSummaryRecord(item: MemoryDocument): MemorySummaryRecord {
  return {
    id: item.id,
    summary: item.content,
    topicLabel: typeof item.metadata?.topic_label === 'string' ? item.metadata.topic_label : null
  };
}

function toImpressionRecord(item: MemoryDocument): ImpressionRecord {
  return {
    id: item.id,
    description: item.content
  };
}

function toFactRecord(item: MemoryDocument): FactRecord {
  return {
    id: item.id,
    content: item.content
  };
}

export async function fetchMemoryWorkspace(target: MemStationTarget, userId?: string): Promise<MemoryWorkspaceData> {
  const [summaries, impressions, facts, keyMemories] = await Promise.all([
    fetchAdminMemoryDocuments(target, { kind: 'summary', userId, limit: 200 }),
    fetchAdminMemoryDocuments(target, { kind: 'impression', userId, limit: 200 }),
    fetchAdminMemoryDocuments(target, { kind: 'fact', userId, limit: 200 }),
    fetchAdminKeyMemories(target, { userId, limit: 200 })
  ]);

  return {
    memorySummaries: summaries.map(toSummaryRecord),
    impressions: impressions.map(toImpressionRecord),
    facts: facts.map(toFactRecord),
    keyMemories
  };
}

export function searchMemories(
  target: MemStationTarget,
  query: string,
  topN = 10,
  userId?: string,
  options?: { dateFrom?: string; dateTo?: string; timeTiers?: string[] }
): Promise<SearchMemoriesResult> {
  return searchAdminMemories(target, {
    query,
    topN,
    userId,
    dateFrom: options?.dateFrom,
    dateTo: options?.dateTo,
    timeTiers: options?.timeTiers
  });
}
