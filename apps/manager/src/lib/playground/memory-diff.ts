import type { ConversationMemoryPayload, KeyMemoryRecord, MemoryDocument, MemoryPreviewPayload } from '#lib/types.ts';

export type MemoryTurnChange = 'added' | 'updated' | 'removed';
export type MemoryTurnKind = 'injected' | 'document' | 'key_memory' | 'preview' | 'keyword';

export interface MemoryTurnDiffEntry {
  id: string;
  kind: MemoryTurnKind;
  change: MemoryTurnChange;
  title: string;
  subtitle?: string;
  before?: string;
  after?: string;
  meta?: Record<string, string>;
}

export interface MemoryTurnDiffSection {
  id: string;
  title: string;
  description: string;
  entries: MemoryTurnDiffEntry[];
}

export interface MemoryTurnDiffResult {
  sections: MemoryTurnDiffSection[];
  changeCount: number;
}

interface SnapshotItem {
  id: string;
  kind: MemoryTurnKind;
  title: string;
  subtitle?: string;
  content: string;
  meta?: Record<string, unknown>;
}

interface MemoryTurnDiffInput {
  memoryPersist: boolean;
  previousInjectedMemory?: Record<string, unknown> | null;
  injectedMemory?: Record<string, unknown> | null;
  previousConversationMemory?: ConversationMemoryPayload | null;
  conversationMemory?: ConversationMemoryPayload | null;
  previousMemoryPreview?: MemoryPreviewPayload | null;
  memoryPreview?: MemoryPreviewPayload | null;
}

export function buildMemoryTurnDiff(input: MemoryTurnDiffInput): MemoryTurnDiffResult {
  const sections: MemoryTurnDiffSection[] = [];

  pushSection(
    sections,
    diffSection({
      id: 'injected',
      title: 'Injected into this turn',
      description: 'Retrieval query and recalled memory items that the gateway injected before the upstream call.',
      before: snapshotInjected(input.previousInjectedMemory),
      after: snapshotInjected(input.injectedMemory)
    })
  );

  if (input.memoryPersist) {
    pushSection(
      sections,
      diffSection({
        id: 'persisted-documents',
        title: 'Persisted documents',
        description: 'Summary, fact, impression, and agent memory records now linked to the conversation after worker processing.',
        before: snapshotConversationDocuments(input.previousConversationMemory),
        after: snapshotConversationDocuments(input.conversationMemory)
      })
    );
    pushSection(
      sections,
      diffSection({
        id: 'persisted-key-memories',
        title: 'Persisted key memories',
        description: 'Key memory writes that became durable for this conversation.',
        before: snapshotConversationKeyMemories(input.previousConversationMemory),
        after: snapshotConversationKeyMemories(input.conversationMemory)
      })
    );
  } else {
    pushSection(
      sections,
      diffSection({
        id: 'preview-documents',
        title: 'Preview extracted documents',
        description: 'Dry-run summary, fact, impression, and agent memory candidates the server would generate for this turn.',
        before: snapshotPreviewDocuments(input.previousMemoryPreview),
        after: snapshotPreviewDocuments(input.memoryPreview)
      })
    );
    pushSection(
      sections,
      diffSection({
        id: 'preview-key-memories',
        title: 'Preview key memories',
        description: 'Dry-run key memory candidates. These are not written while persistence stays off.',
        before: snapshotPreviewKeyMemories(input.previousMemoryPreview),
        after: snapshotPreviewKeyMemories(input.memoryPreview)
      })
    );
    pushSection(
      sections,
      diffSection({
        id: 'preview-keywords',
        title: 'Preview keywords',
        description: 'Keyword candidates inferred from the current conversation turn.',
        before: snapshotPreviewKeywords(input.previousMemoryPreview),
        after: snapshotPreviewKeywords(input.memoryPreview)
      })
    );
  }

  return {
    sections,
    changeCount: sections.reduce((count, section) => count + section.entries.length, 0)
  };
}

function pushSection(sections: MemoryTurnDiffSection[], section: MemoryTurnDiffSection | null): void {
  if (section && section.entries.length > 0) {
    sections.push(section);
  }
}

function diffSection(input: {
  id: string;
  title: string;
  description: string;
  before: SnapshotItem[];
  after: SnapshotItem[];
}): MemoryTurnDiffSection | null {
  const beforeMap = new Map(input.before.map((item) => [item.id, item]));
  const afterMap = new Map(input.after.map((item) => [item.id, item]));
  const ids = Array.from(new Set([...beforeMap.keys(), ...afterMap.keys()]));
  ids.sort();

  const entries = ids
    .map((id) => {
      const before = beforeMap.get(id);
      const after = afterMap.get(id);
      if (!before && !after) {
        return null;
      }
      if (!before && after) {
        return toDiffEntry('added', after);
      }
      if (before && !after) {
        return toDiffEntry('removed', before);
      }
      if (!before || !after) {
        return null;
      }
      if (snapshotSignature(before) === snapshotSignature(after)) {
        return null;
      }
      return {
        id: after.id,
        kind: after.kind,
        change: 'updated',
        title: after.title,
        subtitle: after.subtitle,
        before: before.content,
        after: after.content,
        meta: formatMeta(mergeMeta(before.meta, after.meta))
      } satisfies MemoryTurnDiffEntry;
    })
    .filter((entry): entry is MemoryTurnDiffEntry => entry !== null)
    .sort(sortDiffEntries);

  if (entries.length === 0) {
    return null;
  }

  return {
    id: input.id,
    title: input.title,
    description: input.description,
    entries
  };
}

function toDiffEntry(change: MemoryTurnChange, item: SnapshotItem): MemoryTurnDiffEntry {
  return {
    id: item.id,
    kind: item.kind,
    change,
    title: item.title,
    subtitle: item.subtitle,
    before: change === 'removed' ? item.content : undefined,
    after: change === 'removed' ? undefined : item.content,
    meta: formatMeta(item.meta)
  };
}

function sortDiffEntries(left: MemoryTurnDiffEntry, right: MemoryTurnDiffEntry): number {
  const order = { added: 0, updated: 1, removed: 2 } satisfies Record<MemoryTurnChange, number>;
  if (order[left.change] !== order[right.change]) {
    return order[left.change] - order[right.change];
  }
  return left.title.localeCompare(right.title);
}

function snapshotInjected(payload?: Record<string, unknown> | null): SnapshotItem[] {
  if (!payload) {
    return [];
  }
  const items = asArray(payload.items);
  const snapshots: SnapshotItem[] = [];
  const query = asString(payload.query);
  if (query) {
    snapshots.push({
      id: 'injected:query',
      kind: 'injected',
      title: 'Retrieval query',
      subtitle: 'Gateway search input',
      content: query,
      meta: {
        total: asNumber(payload.total),
        query_embedding_tokens: asNumber(payload.query_embedding_tokens)
      }
    });
  }
  items.forEach((value, index) => {
    const item = asObject(value);
    const id = asString(item.id) || asString(item.node_id) || `injected:item:${index}`;
    const type = asString(item.type) || 'memory';
    snapshots.push({
      id: `injected:item:${id}`,
      kind: 'injected',
      title: `${titleCase(type)} memory`,
      subtitle: id,
      content: asString(item.content),
      meta: {
        type,
        conversation_id: asString(item.conversation_id),
        score: asNumber(item.score),
        effective_score: asNumber(item.effective_score),
        importance: asNumber(item.importance),
        decay_factor: asNumber(item.decay_factor),
        activity_at: asString(item.activity_at),
        time_tier: asString(item.time_tier),
        injection_bucket: asString(item.injection_bucket),
        score_reason: asString(item.score_reason)
      }
    });
  });
  return snapshots;
}

function snapshotConversationDocuments(payload?: ConversationMemoryPayload | null): SnapshotItem[] {
  return (payload?.documents ?? []).map((item) => snapshotDocument(item));
}

function snapshotConversationKeyMemories(payload?: ConversationMemoryPayload | null): SnapshotItem[] {
  return (payload?.key_memories ?? []).map((item) => snapshotKeyMemory(item));
}

function snapshotPreviewDocuments(payload?: MemoryPreviewPayload | null): SnapshotItem[] {
  const snapshots: SnapshotItem[] = [];
  appendPreviewDocument(snapshots, 'summary', payload?.summary);
  const facts = asArray(payload?.facts);
  if (facts.length > 0) {
    for (const fact of facts) {
      appendPreviewDocument(snapshots, 'fact', asObject(fact));
    }
  } else {
    appendPreviewDocument(snapshots, 'fact', payload?.fact);
  }
  appendPreviewDocument(snapshots, 'impression', payload?.impression);
  for (const item of asArray(payload?.generic_items)) {
    appendPreviewDocument(snapshots, asString(asObject(item).type) || 'agent_memory', asObject(item));
  }
  return snapshots;
}

function snapshotPreviewKeyMemories(payload?: MemoryPreviewPayload | null): SnapshotItem[] {
  return asArray(payload?.key_memories).map((value, index) => {
    const item = asObject(value);
    const key = asString(item.key) || `preview-key-${index}`;
    const valueText = asString(item.value);
    return {
      id: `preview:key_memory:${key}`,
      kind: 'key_memory',
      title: key,
      subtitle: 'Preview candidate',
      content: valueText,
      meta: {
        source: asString(item.source),
        category: asString(item.category),
        status: asString(item.status),
        importance: asNumber(item.importance),
        decay_factor: asNumber(item.decay_factor),
        activity_at: asString(item.activity_at),
        time_tier: asString(item.time_tier),
        effective_score: asNumber(item.effective_score)
      }
    } satisfies SnapshotItem;
  });
}

function snapshotPreviewKeywords(payload?: MemoryPreviewPayload | null): SnapshotItem[] {
  return (payload?.keywords ?? []).map((keyword) => ({
    id: `preview:keyword:${keyword}`,
    kind: 'keyword',
    title: keyword,
    subtitle: 'Preview keyword',
    content: keyword
  }));
}

function appendPreviewDocument(target: SnapshotItem[], kind: string, payload?: Record<string, unknown>): void {
  const item = asObject(payload);
  if (Object.keys(item).length === 0) {
    return;
  }
  const id = asString(item.id) || `preview:${kind}`;
  target.push({
    id: `preview:${kind}:${id}`,
    kind: 'preview',
    title: titleCase(kind),
    subtitle: id,
    content: asString(item.content),
    meta: mergeMeta(
      {
        type: asString(item.type)
      },
      asObject(item.metadata)
    )
  });
}

function snapshotDocument(item: MemoryDocument): SnapshotItem {
  return {
    id: `document:${item.id}`,
    kind: 'document',
    title: `${titleCase(item.type)} document`,
    subtitle: item.id,
    content: item.content,
    meta: mergeMeta(item.metadata, {
      node_id: item.node_id,
      conversation_id: item.conversation_id,
      score: item.score,
      effective_score: item.effective_score,
      importance: item.importance,
      decay_factor: item.decay_factor,
      activity_at: item.activity_at,
      time_tier: item.time_tier
    })
  };
}

function snapshotKeyMemory(item: KeyMemoryRecord): SnapshotItem {
  return {
    id: `key_memory:${item.id || item.key}`,
    kind: 'key_memory',
    title: item.key,
    subtitle: item.id,
    content: item.value,
    meta: mergeMeta(item.metadata, {
      category: item.category,
      source: item.source,
      review_status: item.review_status,
      importance: item.importance,
      decay_factor: item.decay_factor,
      activity_at: item.activity_at,
      time_tier: item.time_tier,
      effective_score: item.effective_score,
      updated_at: item.updated_at
    })
  };
}

function mergeMeta(...values: Array<Record<string, unknown> | undefined>): Record<string, unknown> {
  const merged: Record<string, unknown> = {};
  values.forEach((value) => {
    if (!value) {
      return;
    }
    Object.entries(value).forEach(([key, item]) => {
      if (item === undefined || item === null || item === '') {
        return;
      }
      merged[key] = item;
    });
  });
  return merged;
}

function formatMeta(meta?: Record<string, unknown>): Record<string, string> | undefined {
  if (!meta) {
    return undefined;
  }
  const result: Record<string, string> = {};
  Object.entries(meta).forEach(([key, value]) => {
    const formatted = formatScalar(value);
    if (formatted) {
      result[key] = formatted;
    }
  });
  if (Object.keys(result).length === 0) {
    return undefined;
  }
  return result;
}

function snapshotSignature(item: SnapshotItem): string {
  return stableStringify({
    title: item.title,
    subtitle: item.subtitle,
    content: normalizeText(item.content),
    meta: normalizeValue(item.meta ?? {})
  });
}

function stableStringify(value: unknown): string {
  return JSON.stringify(normalizeValue(value));
}

function normalizeValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => normalizeValue(item));
  }
  if (value && typeof value === 'object') {
    const input = value as Record<string, unknown>;
    return Object.keys(input)
      .sort()
      .reduce<Record<string, unknown>>((accumulator, key) => {
        accumulator[key] = normalizeValue(input[key]);
        return accumulator;
      }, {});
  }
  if (typeof value === 'string') {
    return normalizeText(value);
  }
  return value;
}

function normalizeText(value: string): string {
  return value.replace(/\s+/g, ' ').trim();
}

function formatScalar(value: unknown): string {
  if (value === undefined || value === null) {
    return '';
  }
  if (typeof value === 'string') {
    return value.trim();
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => formatScalar(item)).filter(Boolean).join(', ');
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .map(([key, item]) => `${key}: ${formatScalar(item)}`)
      .filter((item) => !item.endsWith(': '));
    return entries.join(' | ');
  }
  return String(value);
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function asNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function asObject(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function titleCase(value: string): string {
  return value
    .split(/[_\s-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
}
