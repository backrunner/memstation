import type {
  AdminConfigPayload,
  AdminSessionState,
  APIKeyRecord,
  AuditEventRecord,
  CacheStatusPayload,
  ChatThreadMessage,
  ContentBlock,
  ConversationListItem,
  ConversationMemoryPayload,
  ConversationRecord,
  ConversationSourcesPayload,
  ConversationSegment,
  DashboardOverview,
  GatewayDebugState,
  GatewayHeaders,
  GatewayMode,
  GatewayProtocol,
  HealthPayload,
  KeyMemoryRecord,
  MemStationTarget,
  MemoryDocument,
  MemoryGeneration,
  MemoryGenerationSource,
  MemoryLineage,
  JSONValue,
  MemoryDocumentKind,
  MemoryRebuildDetailPayload,
  MemorySourceSnapshot,
  MemoryPreviewPayload,
  ProviderDiagnostic,
  QueueInfo,
  QueueTaskDetailPayload,
  QueueTaskRecord,
  RelatedMemory,
  SearchMemoriesResult,
  SearchMemoryType,
  TokenStatsResponse,
  ToolCall,
  UserDetailPayload,
  UserSourcesPayload,
  UserSummary,
  UpdateUserSettingsResponse,
  UsageRecord
} from '$lib/types';

export interface GatewayRequestOptions {
  target: MemStationTarget;
  apiKey: string;
  protocol: GatewayProtocol;
  provider?: string;
  model: string;
  stream: boolean;
  mode: GatewayMode;
  sessionId: string;
  injectContext: boolean;
  injectTools: boolean;
  toolWrite: boolean;
  memoryPersist: boolean;
  endConversation?: boolean;
  messages: ChatThreadMessage[];
  onText?: (delta: string) => void;
  onUsage?: (usage: UsageRecord) => void;
  onRawEvent?: (event: Record<string, unknown>) => void;
}

export interface GatewayExchange {
  headers: GatewayHeaders;
  assistant: ChatThreadMessage;
  debug: GatewayDebugState;
}

export interface RawGatewayRequestOptions {
  target: MemStationTarget;
  apiKey: string;
  protocol: GatewayProtocol;
  provider?: string;
  mode: GatewayMode;
  sessionId: string;
  memoryPersist: boolean;
  injectContext: boolean;
  injectTools: boolean;
  toolWrite: boolean;
  body: Record<string, unknown>;
}

interface GraphQLResponse<T> {
  data?: T;
  errors?: Array<{ message: string }>;
}

function makeId(prefix: string): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}`;
}

function normalizeBaseUrl(value: string): string {
  return value.trim().replace(/\/$/, '');
}

async function readError(response: Response): Promise<string> {
  let body: string;
  try {
    body = await response.text();
  } catch {
    body = response.statusText;
  }

  if (!body) {
    return `${response.status} ${response.statusText}`.trim();
  }

  try {
    const parsed = JSON.parse(body) as { error?: { message?: string } };
    return parsed.error?.message ?? body;
  } catch {
    return body;
  }
}

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init);
  if (!response.ok) {
    throw new Error(await readError(response));
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const raw = await response.text();
  if (!raw.trim()) {
    return undefined as T;
  }

  return JSON.parse(raw) as T;
}

async function adminRequest<T>(target: MemStationTarget, path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  headers.delete('Authorization');
  return requestJSON<T>(`${normalizeBaseUrl(target.baseUrl)}${path}`, {
    ...init,
    credentials: 'include',
    headers
  });
}

export async function loginAdmin(target: Pick<MemStationTarget, 'baseUrl'>, token: string): Promise<{ authenticated: boolean; checkedAt: string }> {
  const payload = await requestJSON<{ authenticated: boolean; checkedAt?: string }>(`${normalizeBaseUrl(target.baseUrl)}/api/admin/login`, {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({ token: token.trim() })
  });
  if (!payload.authenticated) {
    throw new Error('Admin token was rejected by the target.');
  }
  return {
    authenticated: payload.authenticated,
    checkedAt: new Date().toISOString()
  };
}

export async function fetchAdminSession(target: MemStationTarget): Promise<AdminSessionState> {
  if (!target.id || !target.baseUrl) {
    return {
      authenticated: false,
      checking: false,
      checkedAt: new Date().toISOString(),
      error: target.id ? 'Connect this target.' : undefined
    };
  }
  try {
    const payload = await requestJSON<{ authenticated: boolean; error?: string; checkedAt?: string }>(`${normalizeBaseUrl(target.baseUrl)}/api/admin/session`, {
      credentials: 'include',
      method: 'GET'
    });
    return {
      authenticated: payload.authenticated,
      checking: false,
      checkedAt: payload.checkedAt ?? new Date().toISOString(),
      error: payload.error
    };
  } catch (error) {
    return {
      authenticated: false,
      checking: false,
      checkedAt: new Date().toISOString(),
      error: error instanceof Error ? error.message : 'session check failed'
    };
  }
}

export function logoutAdmin(target: Pick<MemStationTarget, 'baseUrl'>): Promise<{ authenticated: boolean }> {
  return requestJSON<{ authenticated: boolean }>(`${normalizeBaseUrl(target.baseUrl)}/api/admin/logout`, {
    method: 'POST',
    credentials: 'include'
  });
}

export function fetchOverview(target: MemStationTarget): Promise<DashboardOverview> {
  return adminRequest(target, '/api/admin/overview');
}

export function fetchTokenStats(target: MemStationTarget): Promise<TokenStatsResponse> {
  return adminRequest(target, '/api/admin/stats/tokens');
}

export async function fetchQueues(target: MemStationTarget): Promise<QueueInfo[]> {
  const payload = await adminRequest<{ queues: QueueInfo[] }>(target, '/api/admin/queues');
  return payload.queues;
}

export async function fetchAudit(
  target: MemStationTarget,
  options?: { limit?: number; actorType?: string; action?: string; targetType?: string; targetId?: string }
): Promise<AuditEventRecord[]> {
  const query = new URLSearchParams();
  if (options?.limit) {
    query.set('limit', String(options.limit));
  }
  if (options?.actorType?.trim()) {
    query.set('actor_type', options.actorType.trim());
  }
  if (options?.action?.trim()) {
    query.set('action', options.action.trim());
  }
  if (options?.targetType?.trim()) {
    query.set('target_type', options.targetType.trim());
  }
  if (options?.targetId?.trim()) {
    query.set('target_id', options.targetId.trim());
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : '';
  const payload = await adminRequest<{ items: AuditEventRecord[] }>(target, `/api/admin/audit${suffix}`);
  return payload.items;
}

export async function fetchConversations(
  target: MemStationTarget,
  options?: { userId?: string; limit?: number }
): Promise<ConversationListItem[]> {
  const query = new URLSearchParams();
  query.set('limit', String(options?.limit ?? 100));
  if (options?.userId?.trim()) {
    query.set('user_id', options.userId.trim());
  }
  const payload = await adminRequest<{ items: ConversationListItem[] }>(target, `/api/admin/conversations?${query.toString()}`);
  return payload.items;
}

export async function fetchConversation(
  target: MemStationTarget,
  conversationId: string,
  userId?: string,
  generationId?: string
): Promise<ConversationRecord> {
  const query = new URLSearchParams();
  if (userId?.trim()) {
    query.set('user_id', userId.trim());
  }
  if (generationId?.trim()) {
    query.set('generation_id', generationId.trim());
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : '';
  const payload = await adminRequest<ConversationRecord>(target, `/api/admin/conversations/${conversationId}${suffix}`);
  return {
    ...payload,
    messages: (payload.messages ?? []).map((message) => normalizeMessage(message as unknown as Record<string, unknown>, target.baseUrl)),
    parts: Array.isArray(payload.parts) ? payload.parts.map((item) => normalizeMessagePart(item as unknown as Record<string, unknown>, target.baseUrl)) : undefined,
    artifacts: Array.isArray(payload.artifacts) ? payload.artifacts.map((item) => normalizeMessagePartArtifact(item as unknown as Record<string, unknown>)) : undefined,
    segments: Array.isArray(payload.segments)
      ? payload.segments.map((segment) => normalizeConversationSegment(segment as unknown as Record<string, unknown>))
      : undefined
  };
}

export function fetchConversationMemory(
  target: MemStationTarget,
  conversationId: string,
  userId?: string,
  generationId?: string
): Promise<ConversationMemoryPayload> {
  const query = new URLSearchParams();
  if (userId?.trim()) {
    query.set('user_id', userId.trim());
  }
  if (generationId?.trim()) {
    query.set('generation_id', generationId.trim());
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : '';
  return adminRequest<{
    conversation_id: string;
    generation_id?: string;
    documents?: Array<Record<string, unknown>>;
    key_memories?: Array<Record<string, unknown>>;
  }>(target, `/api/admin/conversations/${conversationId}/memory${suffix}`).then((payload) => ({
    conversation_id: payload.conversation_id,
    generation_id: typeof payload.generation_id === 'string' ? payload.generation_id : undefined,
    documents: (payload.documents ?? []).map((item) => normalizeMemoryDocument(item)),
    key_memories: (payload.key_memories ?? []).map((item) => normalizeKeyMemoryRecord(item))
  }));
}

export function fetchConversationSources(
  target: MemStationTarget,
  conversationId: string,
  userId?: string
): Promise<ConversationSourcesPayload> {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return adminRequest<{
    user_id: string;
    conversation_id: string;
    has_snapshot: boolean;
    latest_snapshot?: Record<string, unknown>;
    snapshots?: Array<Record<string, unknown>>;
  }>(target, `/api/admin/conversations/${conversationId}/sources${suffix}`).then((payload) => ({
    user_id: payload.user_id,
    conversation_id: payload.conversation_id,
    has_snapshot: Boolean(payload.has_snapshot),
    latest_snapshot: payload.latest_snapshot ? normalizeMemorySourceSnapshot(payload.latest_snapshot) : undefined,
    snapshots: (payload.snapshots ?? []).map((item) => normalizeMemorySourceSnapshot(item))
  }));
}

export function fetchMemoryPreview(
  target: MemStationTarget,
  conversationId: string,
  messages: ChatThreadMessage[]
): Promise<MemoryPreviewPayload> {
  return adminRequest(target, '/api/admin/playground/memory-preview', {
    method: 'POST',
    body: JSON.stringify({
      conversation_id: conversationId,
      messages: messages.map((message) => toUnifiedMessage(message))
    })
  });
}

export function fetchHealth(target: Pick<MemStationTarget, 'baseUrl'>, path: '/health/live' | '/health/ready'): Promise<HealthPayload> {
  return requestJSON<HealthPayload>(`${normalizeBaseUrl(target.baseUrl)}${path}`, {
    credentials: 'omit'
  });
}

export function fetchConfig(target: MemStationTarget): Promise<AdminConfigPayload> {
  return adminRequest(target, '/api/admin/config');
}

export async function fetchProviderDiagnostics(target: MemStationTarget): Promise<ProviderDiagnostic[]> {
  const payload = await adminRequest<{ items: ProviderDiagnostic[] }>(target, '/api/admin/providers/diagnostics');
  return payload.items;
}

export function saveConfig(target: MemStationTarget, payload: Record<string, unknown>): Promise<AdminConfigPayload> {
  return adminRequest(target, '/api/admin/config', {
    method: 'PUT',
    body: JSON.stringify(payload)
  });
}

export async function fetchAdminMemoryDocuments(
  target: MemStationTarget,
  options?: { kind?: MemoryDocumentKind | MemoryDocumentKind[]; userId?: string; limit?: number }
): Promise<MemoryDocument[]> {
  const query = new URLSearchParams();
  if (options?.kind) {
    query.set('kind', Array.isArray(options.kind) ? options.kind.join(',') : options.kind);
  }
  if (options?.userId?.trim()) {
    query.set('user_id', options.userId.trim());
  }
  if (options?.limit) {
    query.set('limit', String(options.limit));
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : '';
  const payload = await adminRequest<{ items: Array<Record<string, unknown>> }>(target, `/api/admin/memory/documents${suffix}`);
  return (payload.items ?? []).map((item) => normalizeMemoryDocument(item));
}

export function fetchAdminMemoryDocument(
  target: MemStationTarget,
  kind: MemoryDocumentKind,
  id: string,
  userId?: string
): Promise<MemoryDocument> {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return adminRequest<Record<string, unknown>>(target, `/api/admin/memory/documents/${encodeURIComponent(kind)}/${encodeURIComponent(id)}${suffix}`).then(
    (payload) => normalizeMemoryDocument(payload)
  );
}

export function updateAdminMemoryDocument(
  target: MemStationTarget,
  kind: MemoryDocumentKind,
  id: string,
  content: string,
  userId?: string
): Promise<MemoryDocument> {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return adminRequest<Record<string, unknown>>(target, `/api/admin/memory/documents/${encodeURIComponent(kind)}/${encodeURIComponent(id)}${suffix}`, {
    method: 'PUT',
    body: JSON.stringify({ content })
  }).then((payload) => normalizeMemoryDocument(payload));
}

export async function searchAdminMemories(
  target: MemStationTarget,
  payload: {
    query: string;
    topN?: number;
    memoryTypes?: SearchMemoryType[];
    minScore?: number;
    userId?: string;
    dateFrom?: string;
    dateTo?: string;
    timeTiers?: string[];
  }
): Promise<SearchMemoriesResult> {
  const suffix = payload.userId?.trim() ? `?user_id=${encodeURIComponent(payload.userId.trim())}` : '';
  const result = await adminRequest<SearchMemoriesResult & { query_embedding_tokens?: number; memories?: Array<Record<string, unknown>> }>(target, `/api/admin/memory/search${suffix}`, {
    method: 'POST',
    body: JSON.stringify({
      query: payload.query,
      top_n: payload.topN ?? 10,
      memory_types: payload.memoryTypes ?? [],
      date_from: payload.dateFrom ?? '',
      date_to: payload.dateTo ?? '',
      time_tiers: payload.timeTiers ?? [],
      min_score: payload.minScore ?? 0
    })
  });
  return {
    total: Number(result.total ?? 0),
    queryEmbeddingTokens: Number(result.queryEmbeddingTokens ?? result.query_embedding_tokens ?? 0),
    memories: (result.memories ?? []).map((item) => normalizeSearchMemoryItem(item as unknown as Record<string, unknown>))
  };
}

export async function fetchAdminKeyMemories(
  target: MemStationTarget,
  options?: { userId?: string; limit?: number; query?: string; dateFrom?: string; dateTo?: string; timeTiers?: string[] }
): Promise<KeyMemoryRecord[]> {
  const query = new URLSearchParams();
  if (options?.userId?.trim()) {
    query.set('user_id', options.userId.trim());
  }
  if (options?.limit) {
    query.set('limit', String(options.limit));
  }
  if (options?.query?.trim()) {
    query.set('query', options.query.trim());
  }
  if (options?.dateFrom?.trim()) {
    query.set('date_from', options.dateFrom.trim());
  }
  if (options?.dateTo?.trim()) {
    query.set('date_to', options.dateTo.trim());
  }
  for (const tier of options?.timeTiers ?? []) {
    if (tier.trim()) {
      query.append('time_tier', tier.trim());
    }
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : '';
  const payload = await adminRequest<{ items: Array<Record<string, unknown>> }>(target, `/api/admin/memory/keys${suffix}`);
  return (payload.items ?? []).map((item) => normalizeKeyMemoryRecord(item));
}

export function fetchAdminKeyMemory(target: MemStationTarget, id: string, userId?: string): Promise<KeyMemoryRecord> {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return adminRequest<Record<string, unknown>>(target, `/api/admin/memory/keys/${encodeURIComponent(id)}${suffix}`).then((payload) =>
    normalizeKeyMemoryRecord(payload)
  );
}

export function createAdminKeyMemory(
  target: MemStationTarget,
  payload: { key: string; value: string; category?: string; reviewStatus?: string; userId?: string }
): Promise<KeyMemoryRecord> {
  const suffix = payload.userId?.trim() ? `?user_id=${encodeURIComponent(payload.userId.trim())}` : '';
  return adminRequest<Record<string, unknown>>(target, `/api/admin/memory/keys${suffix}`, {
    method: 'POST',
    body: JSON.stringify({
      key: payload.key,
      value: payload.value,
      category: payload.category ?? '',
      review_status: payload.reviewStatus ?? 'pending'
    })
  }).then((item) => normalizeKeyMemoryRecord(item));
}

export function updateAdminKeyMemory(
  target: MemStationTarget,
  id: string,
  payload: { value?: string; category?: string; reviewStatus?: string; userId?: string }
): Promise<KeyMemoryRecord> {
  const suffix = payload.userId?.trim() ? `?user_id=${encodeURIComponent(payload.userId.trim())}` : '';
  return adminRequest<Record<string, unknown>>(target, `/api/admin/memory/keys/${encodeURIComponent(id)}${suffix}`, {
    method: 'PUT',
    body: JSON.stringify({
      ...(payload.value !== undefined ? { value: payload.value } : {}),
      ...(payload.category !== undefined ? { category: payload.category } : {}),
      ...(payload.reviewStatus !== undefined ? { review_status: payload.reviewStatus } : {})
    })
  }).then((item) => normalizeKeyMemoryRecord(item));
}

export async function deleteAdminKeyMemory(target: MemStationTarget, id: string, userId?: string): Promise<void> {
  const suffix = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  await adminRequest(target, `/api/admin/memory/keys/${encodeURIComponent(id)}${suffix}`, {
    method: 'DELETE'
  });
}

export async function fetchUsers(target: MemStationTarget): Promise<UserSummary[]> {
  const payload = await adminRequest<{ items: UserSummary[] }>(target, '/api/admin/users');
  return payload.items;
}

export function fetchUserDetail(target: MemStationTarget, userId: string): Promise<UserDetailPayload> {
  return adminRequest(target, `/api/admin/users/${encodeURIComponent(userId)}`);
}

export function fetchUserSources(target: MemStationTarget, userId: string): Promise<UserSourcesPayload> {
  return adminRequest<{
    user_id: string;
    source_stats: {
      snapshot_count: number;
      latest_snapshot_at?: string;
      latest_snapshot_user_id?: string;
    };
    active_generation: Record<string, unknown>;
    generations?: Array<Record<string, unknown>>;
  }>(target, `/api/admin/users/${encodeURIComponent(userId)}/sources`).then((payload) => ({
    user_id: payload.user_id,
    source_stats: payload.source_stats,
    active_generation: normalizeMemoryGeneration(payload.active_generation),
    generations: (payload.generations ?? []).map((item) => normalizeMemoryGeneration(item))
  }));
}

export async function createUserAPIKey(
  target: MemStationTarget,
  userId: string,
  name: string
): Promise<{ record: APIKeyRecord; api_key: string }> {
  return adminRequest(target, `/api/admin/users/${encodeURIComponent(userId)}/api-keys`, {
    method: 'POST',
    body: JSON.stringify({ name })
  });
}

export async function revokeUserAPIKey(target: MemStationTarget, userId: string, keyId: string): Promise<void> {
  await adminRequest(target, `/api/admin/users/${encodeURIComponent(userId)}/api-keys/${encodeURIComponent(keyId)}`, {
    method: 'DELETE'
  });
}

export function updateUserSettings(
  target: MemStationTarget,
  userId: string,
  payload: Record<string, unknown>
): Promise<UpdateUserSettingsResponse> {
  return adminRequest<{
    settings: Record<string, unknown>;
    memory_rebuild_required?: boolean;
  }>(target, `/api/admin/users/${encodeURIComponent(userId)}/settings`, {
    method: 'PUT',
    body: JSON.stringify(payload)
  }).then((result) => ({
    settings: result.settings ?? {},
    memory_rebuild_required: Boolean(result.memory_rebuild_required)
  }));
}

export function fetchMemoryRebuilds(target: MemStationTarget, userId: string, limit = 20): Promise<MemoryGeneration[]> {
  const query = new URLSearchParams({
    user_id: userId,
    limit: String(limit)
  });
  return adminRequest<{ items: Array<Record<string, unknown>> }>(target, `/api/admin/memory/rebuilds?${query.toString()}`).then((payload) =>
    (payload.items ?? []).map((item) => normalizeMemoryGeneration(item))
  );
}

export function fetchMemoryRebuildDetail(target: MemStationTarget, userId: string, generationId: string): Promise<MemoryRebuildDetailPayload> {
  const query = new URLSearchParams({ user_id: userId });
  return adminRequest<{ item: Record<string, unknown>; sources?: Array<Record<string, unknown>> }>(
    target,
    `/api/admin/memory/rebuilds/${encodeURIComponent(generationId)}?${query.toString()}`
  ).then((payload) => ({
    item: normalizeMemoryGeneration(payload.item),
    sources: (payload.sources ?? []).map((item) => normalizeMemoryGenerationSource(item))
  }));
}

export function createMemoryRebuild(
  target: MemStationTarget,
  payload: {
    userId: string;
    scope: 'user' | 'conversation';
    conversationId?: string;
    activateOnSuccess?: boolean;
    embeddingModelOverride?: string;
  }
): Promise<MemoryRebuildDetailPayload> {
  return adminRequest<{ item: Record<string, unknown>; sources?: Array<Record<string, unknown>> }>(target, '/api/admin/memory/rebuilds', {
    method: 'POST',
    body: JSON.stringify({
      user_id: payload.userId,
      scope: payload.scope,
      conversation_id: payload.conversationId ?? '',
      activate_on_success: payload.activateOnSuccess ?? true,
      embedding_model_override: payload.embeddingModelOverride ?? ''
    })
  }).then((result) => ({
    item: normalizeMemoryGeneration(result.item),
    sources: (result.sources ?? []).map((item) => normalizeMemoryGenerationSource(item))
  }));
}

export function activateMemoryRebuild(target: MemStationTarget, userId: string, generationId: string): Promise<MemoryGeneration> {
  return adminRequest<Record<string, unknown>>(target, `/api/admin/memory/rebuilds/${encodeURIComponent(generationId)}/activate`, {
    method: 'POST',
    body: JSON.stringify({ user_id: userId })
  }).then((payload) => normalizeMemoryGeneration(payload));
}

export function discardMemoryRebuild(target: MemStationTarget, userId: string, generationId: string): Promise<MemoryGeneration> {
	const query = new URLSearchParams({ user_id: userId });
	return adminRequest<Record<string, unknown>>(
		target,
		`/api/admin/memory/rebuilds/${encodeURIComponent(generationId)}?${query.toString()}`,
		{ method: 'DELETE' }
	).then((payload) => normalizeMemoryGeneration(payload));
}

export async function fetchTasks(target: MemStationTarget, options?: { limit?: number; states?: string[]; queue?: string }): Promise<QueueTaskRecord[]> {
  const query = new URLSearchParams();
  if (options?.limit) {
    query.set('limit', String(options.limit));
  }
  if (options?.states?.length) {
    query.set('states', options.states.join(','));
  }
  if (options?.queue?.trim()) {
    query.set('queue', options.queue.trim());
  }
  const suffix = query.size > 0 ? `?${query.toString()}` : '';
  const payload = await adminRequest<{ items: QueueTaskRecord[] }>(target, `/api/admin/tasks${suffix}`);
  return payload.items;
}

export function fetchTaskDetail(target: MemStationTarget, queue: string, id: string): Promise<QueueTaskDetailPayload> {
  const query = new URLSearchParams({
    queue,
    id
  });
  return adminRequest(target, `/api/admin/tasks/detail?${query.toString()}`);
}

export function fetchCacheStatus(target: MemStationTarget): Promise<CacheStatusPayload> {
  return adminRequest(target, '/api/admin/cache');
}

export async function requeueTask(
  target: MemStationTarget,
  payload: Record<string, unknown>
): Promise<Record<string, unknown>> {
  return adminRequest(target, '/api/admin/tasks/requeue', {
    method: 'POST',
    body: JSON.stringify(payload)
  });
}

export async function graphqlRequest<T>(
  target: MemStationTarget,
  query: string,
  variables?: Record<string, unknown>
): Promise<T> {
  const response = await adminRequest<GraphQLResponse<T>>(target, '/graphql', {
    method: 'POST',
    body: JSON.stringify({ query, variables })
  });
  if (response.errors?.length) {
    throw new Error(response.errors[0]?.message ?? 'GraphQL request failed');
  }
  if (!response.data) {
    throw new Error('GraphQL response was empty');
  }
  return response.data;
}

export async function requestGatewayExchange(options: GatewayRequestOptions): Promise<GatewayExchange> {
  const { target, protocol, stream } = options;
  const headers = buildGatewayHeaders(options);
  const body = buildGatewayBody(protocol, options.model, options.messages, stream);
  const debug: GatewayDebugState = {
    requestHeaders: headers,
    requestBody: body,
    rawEvents: []
  };

  const response = await fetch(`${normalizeBaseUrl(target.baseUrl)}${gatewayPath(protocol)}`, {
    method: 'POST',
    credentials: 'omit',
    headers,
    body: JSON.stringify(body)
  });

  if (!response.ok) {
    throw new Error(await readError(response));
  }

  const gatewayHeaders = readGatewayHeaders(response.headers);

  if (!stream) {
    const payload = (await response.json()) as Record<string, unknown>;
    debug.rawResponse = payload;
    const assistant = parseGatewayJSON(protocol, payload);
    if (assistant.usage) {
      options.onUsage?.(assistant.usage);
    }
    return { headers: gatewayHeaders, assistant, debug };
  }

  const assistant = await consumeGatewayStream(protocol, response, debug, options);
  return { headers: gatewayHeaders, assistant, debug };
}

export function requestGatewayRaw(options: RawGatewayRequestOptions): Promise<Response> {
  return fetch(`${normalizeBaseUrl(options.target.baseUrl)}${gatewayPath(options.protocol)}`, {
    method: 'POST',
    credentials: 'omit',
    headers: buildRawGatewayHeaders(options),
    body: JSON.stringify(options.body)
  });
}

function buildGatewayHeaders(options: GatewayRequestOptions): Record<string, string> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${options.apiKey}`,
    'Content-Type': 'application/json',
    'X-MemStation-Mode': options.mode,
    'X-MemStation-Session': options.sessionId,
    'X-MemStation-Inject-Context': String(options.injectContext),
    'X-MemStation-Inject-Tools': String(options.injectTools),
    'X-MemStation-Tool-Write': String(options.toolWrite),
    'X-MemStation-Memory-Persist': String(options.memoryPersist),
    'X-MemStation-End-Conversation': String(Boolean(options.endConversation))
  };
  if (options.provider?.trim()) {
    headers['X-MemStation-Provider'] = options.provider.trim();
  }
  return headers;
}

function buildRawGatewayHeaders(options: RawGatewayRequestOptions): Record<string, string> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${options.apiKey}`,
    'Content-Type': 'application/json',
    'X-MemStation-Mode': options.mode,
    'X-MemStation-Session': options.sessionId,
    'X-MemStation-Memory-Persist': String(options.memoryPersist),
    'X-MemStation-Inject-Context': String(options.injectContext),
    'X-MemStation-Inject-Tools': String(options.injectTools),
    'X-MemStation-Tool-Write': String(options.toolWrite)
  };
  if (options.provider?.trim()) {
    headers['X-MemStation-Provider'] = options.provider.trim();
  }
  return headers;
}

function buildGatewayBody(
  protocol: GatewayProtocol,
  model: string,
  messages: ChatThreadMessage[],
  stream: boolean
): Record<string, unknown> {
  switch (protocol) {
    case 'responses':
      return {
        model,
        stream,
        input: responsesInput(messages)
      };
    case 'anthropic_messages':
      return {
        model,
        stream,
        messages: anthropicMessages(messages)
      };
    case 'chat_completions':
    default:
      return {
        model,
        stream,
        messages: chatMessages(messages)
      };
  }
}

function chatMessages(messages: ChatThreadMessage[]): Array<Record<string, unknown>> {
  return messages.map((message) => {
    if (message.role === 'tool') {
      const toolResult = message.toolResults?.[0];
      return {
        role: 'tool',
        tool_call_id: toolResult?.tool_call_id ?? '',
        content: toolResult?.output ?? openAIChatContent(message)
      };
    }
    const payload: Record<string, unknown> = {
      role: message.role,
      content: openAIChatContent(message)
    };
    if (message.toolCalls?.length) {
      payload.tool_calls = message.toolCalls.map((call) => ({
        id: call.id,
        type: 'function',
        function: {
          name: call.name,
          arguments: serializeJSONValue(call.arguments, '{}')
        }
      }));
    }
    return payload;
  });
}

function responsesInput(messages: ChatThreadMessage[]): Array<Record<string, unknown>> {
  const items: Array<Record<string, unknown>> = [];
  for (const message of messages) {
    const content = openAIResponsesContent(message, false);
    if (message.role === 'tool') {
      const toolResult = message.toolResults?.[0];
      items.push({
        type: 'function_call_output',
        call_id: toolResult?.tool_call_id ?? '',
        output: toolResult?.output ?? message.text
      });
      continue;
    }
    if (message.toolCalls?.length) {
      for (const call of message.toolCalls) {
        items.push({
          type: 'function_call',
          id: call.id,
          call_id: call.id,
          name: call.name,
          arguments: serializeJSONValue(call.arguments, '{}')
        });
      }
      if (content.length === 0) {
        continue;
      }
    }
    items.push({
      type: 'message',
      role: message.role,
      content
    });
  }
  return items;
}

function anthropicMessages(messages: ChatThreadMessage[]): Array<Record<string, unknown>> {
  return messages.map((message) => {
    if (message.role === 'tool') {
      const toolResult = message.toolResults?.[0];
      return {
        role: 'user',
        content: [
          {
            type: 'tool_result',
            tool_use_id: toolResult?.tool_call_id ?? '',
            content: toolResult?.output ?? anthropicContent(message)
          }
        ]
      };
    }

    const content = anthropicContentItems(message);
    for (const call of message.toolCalls ?? []) {
      content.push({
        type: 'tool_use',
        id: call.id,
        name: call.name,
        input: call.arguments ?? {}
      });
    }

    return {
      role: message.role,
      content: content.length === 1 && content[0]?.type === 'text' ? String(content[0].text ?? message.text) : content
    };
  });
}

function gatewayPath(protocol: GatewayProtocol): string {
  switch (protocol) {
    case 'responses':
      return '/v1/responses';
    case 'anthropic_messages':
      return '/v1/messages';
    case 'chat_completions':
    default:
      return '/v1/chat/completions';
  }
}

function readGatewayHeaders(headers: Headers): GatewayHeaders {
  return {
    conversationId: headers.get('X-MemStation-Conversation-ID') ?? '',
    requestId: headers.get('X-MemStation-Request-ID') ?? '',
    memoryPersist: headers.get('X-MemStation-Memory-Persist') !== 'false',
    sourceRetain: headers.get('X-MemStation-Source-Retain') === 'true'
  };
}

function parseGatewayJSON(protocol: GatewayProtocol, payload: Record<string, unknown>): ChatThreadMessage {
  switch (protocol) {
    case 'responses':
      return parseResponsesPayload(payload);
    case 'anthropic_messages':
      return parseAnthropicPayload(payload);
    case 'chat_completions':
    default:
      return parseChatPayload(payload);
  }
}

function parseChatPayload(payload: Record<string, unknown>): ChatThreadMessage {
  const choice = asRecord(asArray(payload.choices)[0]);
  const message = asRecord(choice.message);
  const blocks = gatewayBlocks(message.content);
  return {
    id: String(payload.id ?? makeId('chat')),
    role: 'assistant',
    text: blocksText(blocks) || contentText(message.content),
    blocks: blocks.length > 0 ? blocks : undefined,
    thinking: thinkingText(message.reasoning ?? message.thinking),
    toolCalls: openAIToolCalls(message.tool_calls),
    usage: normalizeUsage(asRecord(payload.usage))
  };
}

function parseResponsesPayload(payload: Record<string, unknown>): ChatThreadMessage {
  const output = asArray(payload.output);
  const toolCalls: ToolCall[] = [];
  const blocks: ContentBlock[] = [];
  let text = '';
  let thinking = '';
  for (const item of output) {
    const record = asRecord(item);
    if (record.type === 'function_call') {
      toolCalls.push({
        id: String(record.call_id ?? record.id ?? makeId('tool-call')),
        name: String(record.name ?? ''),
        arguments: parseJSONValue(record.arguments, {})
      });
      continue;
    }
    if (isThinkingType(record.type)) {
      thinking += thinkingText(record);
      continue;
    }
    if (record.type === 'message') {
      const contentBlocks = gatewayBlocks(record.content);
      blocks.push(...contentBlocks);
      text += blocksText(contentBlocks) || contentText(record.content);
      thinking += thinkingText(record.content);
    }
  }
  return {
    id: String(payload.id ?? makeId('response')),
    role: 'assistant',
    text,
    blocks: blocks.length > 0 ? blocks : undefined,
    thinking,
    toolCalls,
    usage: normalizeUsage(asRecord(payload.usage))
  };
}

function parseAnthropicPayload(payload: Record<string, unknown>): ChatThreadMessage {
  const content = asArray(payload.content);
  const toolCalls: ToolCall[] = [];
  const blocks: ContentBlock[] = [];
  let text = '';
  let thinking = '';
  for (const item of content) {
    const record = asRecord(item);
    if (record.type === 'tool_use') {
      toolCalls.push({
        id: String(record.id ?? makeId('anthropic-tool')),
        name: String(record.name ?? ''),
        arguments: jsonValue(record.input)
      });
      continue;
    }
    if (record.type === 'thinking' || record.type === 'redacted_thinking') {
      thinking += thinkingText(record);
      continue;
    }
    const contentBlocks = gatewayBlocks(record);
    blocks.push(...contentBlocks);
    text += blocksText(contentBlocks) || contentText(record);
  }
  return {
    id: String(payload.id ?? makeId('anthropic')),
    role: 'assistant',
    text,
    blocks: blocks.length > 0 ? blocks : undefined,
    thinking,
    toolCalls,
    usage: normalizeUsage(asRecord(payload.usage))
  };
}

async function consumeGatewayStream(
  protocol: GatewayProtocol,
  response: Response,
  debug: GatewayDebugState,
  options: GatewayRequestOptions
): Promise<ChatThreadMessage> {
  const assistant: ChatThreadMessage = {
    id: makeId('stream'),
    role: 'assistant',
    text: '',
    toolCalls: [],
    pending: false
  };

  if (!response.body) {
    return assistant;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let eventName = '';

  const processFrame = (frame: string) => {
    const lines = frame.split(/\r?\n/);
    let data = '';
    let namedEvent = eventName;
    for (const line of lines) {
      if (!line) {
        continue;
      }
      if (line.startsWith('event:')) {
        namedEvent = line.slice(6).trim();
        continue;
      }
      if (line.startsWith('data:')) {
        data += line.slice(5).trim();
      }
    }
    eventName = '';
    if (!data || data === '[DONE]') {
      return;
    }
    const parsed = asRecord(parseJSONValue(data, {}));
    if (Object.keys(parsed).length === 0) {
      return;
    }
    debug.rawEvents.push(parsed);
    options.onRawEvent?.(parsed);
    applyStreamEvent(protocol, namedEvent, parsed, assistant, options);
  };

  while (true) {
    const { done, value } = await reader.read();
    buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done });

    let separatorIndex = buffer.indexOf('\n\n');
    while (separatorIndex >= 0) {
      const frame = buffer.slice(0, separatorIndex);
      buffer = buffer.slice(separatorIndex + 2);
      processFrame(frame);
      separatorIndex = buffer.indexOf('\n\n');
    }

    if (done) {
      if (buffer.trim()) {
        processFrame(buffer.trim());
      }
      break;
    }
  }

  return assistant;
}

function applyStreamEvent(
  protocol: GatewayProtocol,
  namedEvent: string,
  parsed: Record<string, unknown>,
  assistant: ChatThreadMessage,
  options: GatewayRequestOptions
): void {
  switch (protocol) {
    case 'responses': {
      const type = String(parsed.type ?? namedEvent);
      if (type === 'response.output_text.delta') {
        const delta = String(parsed.delta ?? '');
        assistant.text += delta;
        options.onText?.(delta);
        return;
      }
      if (isThinkingType(type)) {
        const delta = thinkingText(parsed.delta ?? parsed);
        assistant.thinking = appendChunk(assistant.thinking, delta);
      }
      return;
    }
    case 'anthropic_messages': {
      const eventType = String(parsed.type ?? namedEvent);
      if (eventType === 'content_block_delta') {
        const delta = asRecord(parsed.delta);
        if (isThinkingType(delta.type) || isThinkingType(namedEvent)) {
          assistant.thinking = appendChunk(assistant.thinking, thinkingText(delta));
          return;
        }
        const text = contentText(delta);
        assistant.text += text;
        options.onText?.(text);
        return;
      }
      if (eventType === 'content_block_start') {
        const block = asRecord(parsed.content_block);
        if (isThinkingType(block.type)) {
          assistant.thinking = appendChunk(assistant.thinking, thinkingText(block));
        }
      }
      return;
    }
    case 'chat_completions':
    default: {
      const choices = asArray(parsed.choices);
      const firstChoice = asRecord(choices[0]);
      const delta = asRecord(firstChoice.delta);
      const thinking = thinkingText(delta.reasoning ?? delta.thinking);
      if (thinking) {
        assistant.thinking = appendChunk(assistant.thinking, thinking);
      }
      if (delta.content) {
        const text = contentText(delta.content);
        assistant.text += text;
        options.onText?.(text);
      }
      if (parsed.usage) {
        const usage = normalizeUsage(asRecord(parsed.usage));
        assistant.usage = usage;
        options.onUsage?.(usage);
      }
    }
  }
}

function normalizeUsage(payload: Record<string, unknown>): UsageRecord {
  return {
    input_tokens: numberValue(payload.input_tokens) ?? numberValue(payload.prompt_tokens) ?? 0,
    output_tokens: numberValue(payload.output_tokens) ?? numberValue(payload.completion_tokens) ?? 0,
    total_tokens: numberValue(payload.total_tokens) ?? 0
  };
}

function normalizeMessage(payload: Record<string, unknown>, baseUrl?: string): ChatThreadMessage {
  return {
    id: String(payload.id ?? makeId('message')),
    role: String(payload.role ?? 'assistant') as ChatThreadMessage['role'],
    text: String(payload.text ?? ''),
    blocks: Array.isArray(payload.blocks)
      ? payload.blocks.map((item) => normalizeContentBlock(item as Record<string, unknown>, baseUrl))
      : undefined,
    toolCalls: asArray(payload.tool_calls).map((item) => {
      const record = asRecord(item);
      return {
        id: String(record.id ?? makeId('tool-call')),
        name: String(record.name ?? ''),
        arguments: jsonValue(record.arguments)
      };
    }),
    toolResults: asArray(payload.tool_results).map((item) => {
      const record = asRecord(item);
      return {
        tool_call_id: String(record.tool_call_id ?? ''),
        name: typeof record.name === 'string' ? record.name : undefined,
        output: jsonValue(record.output)
      };
    }),
    thinking: thinkingText(payload.thinking ?? payload.metadata),
    metadata: asRecord(payload.metadata)
  };
}

function normalizeMemoryDocument(payload: Record<string, unknown>): MemoryDocument {
  const lineage = lineageOrUndefined(payload.lineage) ?? lineageFromMetadata(objectOrUndefined(payload.metadata));
  return {
    id: String(payload.id ?? ''),
    node_id: stringOrUndefined(payload.node_id) ?? '',
    generation_id: stringOrUndefined(payload.generation_id),
    user_id: stringOrUndefined(payload.user_id) ?? '',
    conversation_id: stringOrUndefined(payload.conversation_id),
    type: String(payload.type ?? ''),
    content: String(payload.content ?? ''),
    score: numberOrUndefined(payload.score),
    effective_score: numberOrUndefined(payload.effective_score ?? payload.effectiveScore),
    importance: numberOrUndefined(payload.importance),
    decay_factor: numberOrUndefined(payload.decay_factor),
    activity_at: stringOrUndefined(payload.activity_at),
    time_tier: stringOrUndefined(payload.time_tier),
    metadata: objectOrUndefined(payload.metadata),
    lineage,
    related: Array.isArray(payload.related) ? payload.related.map((value) => normalizeRelatedMemory(value as Record<string, unknown>)) : undefined,
    evidences: Array.isArray(payload.evidences) ? payload.evidences.map((value) => normalizeMemoryEvidence(value as Record<string, unknown>)) : undefined,
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at)
  };
}

function normalizeRelatedMemory(payload: Record<string, unknown>): RelatedMemory {
  return {
    weight: Number(payload.weight ?? 0),
    node_id: stringOrUndefined(payload.node_id),
    document_id: stringOrUndefined(payload.document_id),
    document_type: stringOrUndefined(payload.document_type),
    relation_type: stringOrUndefined(payload.relation_type)
  };
}

function normalizeContentBlock(payload: Record<string, unknown>, baseUrl?: string) {
  return {
    type: String(payload.type ?? ''),
    mime_type: stringOrUndefined(payload.mime_type),
    blob_id: stringOrUndefined(payload.blob_id),
    source_url: absoluteUrl(stringOrUndefined(payload.source_url), baseUrl),
    provider_file_id: stringOrUndefined(payload.provider_file_id),
    filename: stringOrUndefined(payload.filename),
    size_bytes: numberOrUndefined(payload.size_bytes),
    sha256: stringOrUndefined(payload.sha256),
    page_index: numberOrUndefined(payload.page_index),
    start_ms: numberOrUndefined(payload.start_ms),
    end_ms: numberOrUndefined(payload.end_ms),
    duration_ms: numberOrUndefined(payload.duration_ms),
    text: stringOrUndefined(payload.text),
    raw: stringOrUndefined(payload.raw),
    metadata: objectOrUndefined(payload.metadata)
  };
}

function normalizeMessagePart(payload: Record<string, unknown>, baseUrl?: string) {
  return {
    part_id: String(payload.part_id ?? ''),
    user_id: String(payload.user_id ?? ''),
    conversation_id: String(payload.conversation_id ?? ''),
    sequence_num: Number(payload.sequence_num ?? 0),
    part_index: Number(payload.part_index ?? 0),
    role: String(payload.role ?? 'assistant') as ChatThreadMessage['role'],
    part_type: String(payload.part_type ?? ''),
    mime_type: stringOrUndefined(payload.mime_type),
    blob_id: stringOrUndefined(payload.blob_id),
    source_url: absoluteUrl(stringOrUndefined(payload.source_url), baseUrl),
    provider_file_id: stringOrUndefined(payload.provider_file_id),
    filename: stringOrUndefined(payload.filename),
    text_content: stringOrUndefined(payload.text_content),
    raw_payload: stringOrUndefined(payload.raw_payload),
    page_index: numberOrUndefined(payload.page_index),
    start_ms: numberOrUndefined(payload.start_ms),
    end_ms: numberOrUndefined(payload.end_ms),
    duration_ms: numberOrUndefined(payload.duration_ms),
    metadata: objectOrUndefined(payload.metadata),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at)
  };
}

function normalizeMessagePartArtifact(payload: Record<string, unknown>) {
  return {
    artifact_id: String(payload.artifact_id ?? ''),
    user_id: String(payload.user_id ?? ''),
    conversation_id: String(payload.conversation_id ?? ''),
    part_id: String(payload.part_id ?? ''),
    artifact_type: String(payload.artifact_type ?? ''),
    status: String(payload.status ?? ''),
    extractor_kind: stringOrUndefined(payload.extractor_kind),
    model_name: stringOrUndefined(payload.model_name),
    text_content: stringOrUndefined(payload.text_content),
    page_index: numberOrUndefined(payload.page_index),
    start_ms: numberOrUndefined(payload.start_ms),
    end_ms: numberOrUndefined(payload.end_ms),
    confidence: numberOrUndefined(payload.confidence),
    metadata: objectOrUndefined(payload.metadata),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at)
  };
}

function normalizeMemoryEvidence(payload: Record<string, unknown>) {
  return {
    id: String(payload.id ?? ''),
    generation_id: stringOrUndefined(payload.generation_id),
    user_id: String(payload.user_id ?? ''),
    node_id: String(payload.node_id ?? ''),
    conversation_id: String(payload.conversation_id ?? ''),
    sequence_num: Number(payload.sequence_num ?? 0),
    part_id: stringOrUndefined(payload.part_id),
    artifact_id: stringOrUndefined(payload.artifact_id),
    page_index: numberOrUndefined(payload.page_index),
    start_ms: numberOrUndefined(payload.start_ms),
    end_ms: numberOrUndefined(payload.end_ms),
    evidence_kind: String(payload.evidence_kind ?? ''),
    excerpt_text: stringOrUndefined(payload.excerpt_text),
    weight: numberOrUndefined(payload.weight),
    metadata: objectOrUndefined(payload.metadata),
    created_at: stringOrUndefined(payload.created_at)
  };
}

function normalizeConversationSegment(payload: Record<string, unknown>): ConversationSegment {
  return {
    id: String(payload.id ?? ''),
    generation_id: stringOrUndefined(payload.generation_id),
    user_id: stringOrUndefined(payload.user_id) ?? '',
    conversation_id: stringOrUndefined(payload.conversation_id) ?? '',
    segment_index: Number(payload.segment_index ?? 0),
    start_sequence: Number(payload.start_sequence ?? 0),
    end_sequence: Number(payload.end_sequence ?? 0),
    message_count: Number(payload.message_count ?? 0),
    overlap_from_sequence: Number(payload.overlap_from_sequence ?? 0),
    overlap_message_count: Number(payload.overlap_message_count ?? 0),
    content: String(payload.content ?? ''),
    content_hash: String(payload.content_hash ?? ''),
    metadata: objectOrUndefined(payload.metadata),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at)
  };
}

function normalizeSearchMemoryItem(payload: Record<string, unknown>) {
  const item = normalizeMemoryDocument(payload);
  return {
    id: item.id,
    type: item.type,
    content: item.content,
    score: Number(item.score ?? 0),
    effectiveScore: Number(item.effective_score ?? 0),
    node_id: item.node_id,
    conversation_id: item.conversation_id,
    importance: item.importance,
    decay_factor: item.decay_factor,
    activity_at: item.activity_at,
    time_tier: item.time_tier,
    metadata: item.metadata,
    lineage: item.lineage,
    related: item.related
  };
}

function normalizeMemorySourceSnapshot(payload: Record<string, unknown>): MemorySourceSnapshot {
  return {
    snapshot_id: String(payload.snapshot_id ?? ''),
    user_id: String(payload.user_id ?? ''),
    conversation_id: String(payload.conversation_id ?? ''),
    snapshot_version: Number(payload.snapshot_version ?? 0),
    source_hash: String(payload.source_hash ?? ''),
    request_format: stringOrUndefined(payload.request_format),
    provider: stringOrUndefined(payload.provider),
    mode: stringOrUndefined(payload.mode),
    finalize_status: stringOrUndefined(payload.finalize_status),
    message_count: numberOrUndefined(payload.message_count),
    metadata: objectOrUndefined(payload.metadata),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at)
  };
}

function normalizeMemoryGeneration(payload: Record<string, unknown>): MemoryGeneration {
  return {
    generation_id: String(payload.generation_id ?? ''),
    user_id: String(payload.user_id ?? ''),
    status: String(payload.status ?? ''),
    scope: String(payload.scope ?? ''),
    conversation_id: stringOrUndefined(payload.conversation_id),
    is_active: Boolean(payload.is_active),
    structure_version: Number(payload.structure_version ?? 0),
    embedding_version: Number(payload.embedding_version ?? 0),
    embedding_model: String(payload.embedding_model ?? ''),
    activate_on_success: Boolean(payload.activate_on_success),
    source_count: Number(payload.source_count ?? 0),
    processed_count: Number(payload.processed_count ?? 0),
    embedding_count: Number(payload.embedding_count ?? 0),
    failure_count: Number(payload.failure_count ?? 0),
    last_error: stringOrUndefined(payload.last_error),
    metadata: objectOrUndefined(payload.metadata),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at),
    activated_at: stringOrUndefined(payload.activated_at)
  };
}

function normalizeMemoryGenerationSource(payload: Record<string, unknown>): MemoryGenerationSource {
  return {
    id: String(payload.id ?? ''),
    generation_id: String(payload.generation_id ?? ''),
    user_id: String(payload.user_id ?? ''),
    snapshot_id: String(payload.snapshot_id ?? ''),
    conversation_id: String(payload.conversation_id ?? ''),
    source_hash: String(payload.source_hash ?? ''),
    status: String(payload.status ?? ''),
    last_error: stringOrUndefined(payload.last_error),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at),
    processed_at: stringOrUndefined(payload.processed_at)
  };
}

function normalizeKeyMemoryRecord(payload: Record<string, unknown>): KeyMemoryRecord {
  return {
    id: String(payload.id ?? ''),
    user_id: stringOrUndefined(payload.user_id),
    key: String(payload.key ?? ''),
    value: String(payload.value ?? ''),
    category: stringOrUndefined(payload.category),
    source: stringOrUndefined(payload.source),
    review_status: stringOrUndefined(payload.review_status ?? payload.review),
    importance: numberOrUndefined(payload.importance),
    decay_factor: numberOrUndefined(payload.decay_factor),
    activity_at: stringOrUndefined(payload.activity_at),
    time_tier: stringOrUndefined(payload.time_tier),
    effective_score: numberOrUndefined(payload.effective_score ?? payload.effectiveScore),
    last_confirmed_at: stringOrUndefined(payload.last_confirmed_at),
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at),
    metadata: objectOrUndefined(payload.metadata)
  };
}

function stringOrUndefined(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function numberOrUndefined(value: unknown): number | undefined {
  return typeof value === 'number' && !Number.isNaN(value) ? value : undefined;
}

function objectOrUndefined(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return undefined;
  }
  return value as Record<string, unknown>;
}

function lineageOrUndefined(value: unknown): MemoryLineage | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return undefined;
  }
  const record = value as Record<string, unknown>;
  const lineage = lineageFromMetadata(record);
  return lineage && Object.values(lineage).some((entry) => entry !== undefined && entry !== null && entry !== '' && !(Array.isArray(entry) && entry.length === 0))
    ? lineage
    : undefined;
}

function lineageFromMetadata(metadata?: Record<string, unknown>): MemoryLineage | undefined {
  if (!metadata) {
    return undefined;
  }
  const lineagePath = asStringArray(metadata.lineage_path ?? metadata.lineagePath);
  const aliases = asStringArray(metadata.superseded_alias_node_ids ?? metadata.supersededAliasNodeIDs);
  const lineage: MemoryLineage = {
    fact_key: stringOrUndefined(metadata.fact_key ?? metadata.factKey),
    lineage_group_id: stringOrUndefined(metadata.lineage_group_id ?? metadata.lineageGroupID),
    revision_seq: numberOrUndefined(metadata.revision_seq ?? metadata.revisionSeq),
    generation_id: stringOrUndefined(metadata.generation_id ?? metadata.generationID),
    lineage_path: lineagePath.length > 0 ? lineagePath : undefined,
    superseded_alias_node_ids: aliases.length > 0 ? aliases : undefined,
    revision_depth: numberOrUndefined(metadata.revision_depth ?? metadata.revisionDepth),
    current_head_id: stringOrUndefined(metadata.current_head_id ?? metadata.currentHeadID)
  };
  return Object.values(lineage).some((entry) => entry !== undefined && entry !== null && entry !== '' && !(Array.isArray(entry) && entry.length === 0))
    ? lineage
    : undefined;
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is string => typeof item === 'string' && item.length > 0);
}

function absoluteUrl(value: string | undefined, baseUrl?: string): string | undefined {
  if (!value) {
    return undefined;
  }
  if (/^https?:\/\//i.test(value)) {
    return value;
  }
  if (!baseUrl) {
    return value;
  }
  return `${normalizeBaseUrl(baseUrl)}${value.startsWith('/') ? value : `/${value}`}`;
}

function openAIToolCalls(raw: unknown): ToolCall[] {
  return asArray(raw).map((item) => {
    const record = asRecord(item);
    const fn = asRecord(record.function);
    return {
      id: String(record.id ?? makeId('tool-call')),
      name: String(fn.name ?? ''),
      arguments: parseJSONValue(fn.arguments, {})
    };
  });
}

function toUnifiedMessage(message: ChatThreadMessage): Record<string, unknown> {
  const result: Record<string, unknown> = {
    role: message.role,
    text: message.text
  };
  if (message.blocks?.length) {
    result.blocks = message.blocks;
  }
  if (message.toolCalls?.length) {
    result.tool_calls = message.toolCalls;
  }
  if (message.toolResults?.length) {
    result.tool_results = message.toolResults;
  }
  if (message.metadata) {
    result.metadata = message.metadata;
  }
  return result;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || Array.isArray(value) || typeof value !== 'object') {
    return {};
  }
  return value as Record<string, unknown>;
}

function jsonValue(value: unknown): JSONValue | undefined {
  if (value === undefined) {
    return undefined;
  }
  return value as JSONValue;
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === 'number') {
    return value;
  }
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  return undefined;
}

function parseJSONValue(value: unknown, fallback: JSONValue): JSONValue {
  if (value === undefined || value === null) {
    return fallback;
  }
  if (typeof value !== 'string') {
    return value as JSONValue;
  }
  try {
    return JSON.parse(value) as JSONValue;
  } catch {
    return value as JSONValue;
  }
}

function serializeJSONValue(value: JSONValue | undefined, fallback: string): string {
  if (value === undefined) {
    return fallback;
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      return value;
    }
  }
  try {
    const serialized = JSON.stringify(value);
    return serialized ?? fallback;
  } catch {
    return fallback;
  }
}

function appendChunk(current: string | undefined, chunk: string): string {
  if (!chunk) {
    return current ?? '';
  }
  return `${current ?? ''}${chunk}`;
}

function canonicalBlocks(message: ChatThreadMessage): ContentBlock[] {
  if (message.blocks?.length) {
    return message.blocks;
  }
  if (message.text.trim()) {
    return [{ type: 'text', text: message.text }];
  }
  return [];
}

function openAIChatContent(message: ChatThreadMessage): string | Array<Record<string, unknown>> {
  const blocks = canonicalBlocks(message);
  if (blocks.length === 0) {
    return message.text;
  }
  if (blocks.length === 1 && blocks[0]?.type === 'text') {
    return blocks[0].text ?? message.text;
  }
  const items = blocks.map((block) => openAIContentBlock(block, false)).filter((item): item is Record<string, unknown> => Boolean(item));
  return items.length > 0 ? items : [{ type: 'input_text', text: message.text }];
}

function openAIResponsesContent(message: ChatThreadMessage, output: boolean): Array<Record<string, unknown>> {
  const blocks = canonicalBlocks(message);
  if (blocks.length === 0) {
    return message.text.trim() ? [{ type: output ? 'output_text' : 'input_text', text: message.text }] : [];
  }
  return blocks.map((block) => openAIContentBlock(block, output)).filter((item): item is Record<string, unknown> => Boolean(item));
}

function anthropicContent(message: ChatThreadMessage): string | Array<Record<string, unknown>> {
  const items = anthropicContentItems(message);
  if (items.length === 0) {
    return message.text;
  }
  if (items.length === 1 && items[0]?.type === 'text') {
    return String(items[0].text ?? message.text);
  }
  return items;
}

function anthropicContentItems(message: ChatThreadMessage): Array<Record<string, unknown>> {
  const blocks = canonicalBlocks(message);
  if (blocks.length === 0) {
    return message.text.trim() ? [{ type: 'text', text: message.text }] : [];
  }
  const items = blocks.map((block) => anthropicContentBlock(block)).filter((item): item is Record<string, unknown> => Boolean(item));
  return items;
}

function openAIContentBlock(block: ContentBlock, output: boolean): Record<string, unknown> | null {
  switch (block.type) {
    case 'text':
      return {
        type: output ? 'output_text' : 'input_text',
        text: block.text ?? block.raw ?? ''
      };
    case 'image': {
      const item: Record<string, unknown> = { type: 'input_image' };
      if (block.mime_type) {
        item.image_url = { url: block.source_url, mime_type: block.mime_type };
      } else if (block.source_url) {
        item.image_url = block.source_url;
      }
      if (block.provider_file_id) {
        item.file_id = block.provider_file_id;
      }
      if (block.blob_id) {
        item.blob_id = block.blob_id;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.text) {
        item.text = block.text;
      }
      return item;
    }
    case 'audio': {
      const item: Record<string, unknown> = { type: 'input_audio' };
      const audio: Record<string, unknown> = {};
      if (block.source_url) {
        audio.url = block.source_url;
      }
      if (block.provider_file_id) {
        audio.file_id = block.provider_file_id;
      }
      if (block.mime_type) {
        audio.mime_type = block.mime_type;
      }
      if (Object.keys(audio).length > 0) {
        item.audio = audio;
      }
      if (block.blob_id) {
        item.blob_id = block.blob_id;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.text) {
        item.text = block.text;
      }
      if (block.start_ms !== undefined) {
        item.start_ms = block.start_ms;
      }
      if (block.end_ms !== undefined) {
        item.end_ms = block.end_ms;
      }
      if (block.duration_ms !== undefined) {
        item.duration_ms = block.duration_ms;
      }
      return item;
    }
    case 'video': {
      const item: Record<string, unknown> = { type: 'input_video' };
      const video: Record<string, unknown> = {};
      if (block.source_url) {
        video.url = block.source_url;
      }
      if (block.provider_file_id) {
        video.file_id = block.provider_file_id;
      }
      if (block.mime_type) {
        video.mime_type = block.mime_type;
      }
      if (Object.keys(video).length > 0) {
        item.video = video;
      }
      if (block.blob_id) {
        item.blob_id = block.blob_id;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.text) {
        item.text = block.text;
      }
      if (block.start_ms !== undefined) {
        item.start_ms = block.start_ms;
      }
      if (block.end_ms !== undefined) {
        item.end_ms = block.end_ms;
      }
      if (block.duration_ms !== undefined) {
        item.duration_ms = block.duration_ms;
      }
      return item;
    }
    case 'file': {
      const item: Record<string, unknown> = { type: 'file' };
      if (block.source_url) {
        item.source_url = block.source_url;
      }
      if (block.provider_file_id) {
        item.file_id = block.provider_file_id;
      }
      if (block.blob_id) {
        item.blob_id = block.blob_id;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.mime_type) {
        item.mime_type = block.mime_type;
      }
      if (block.page_index !== undefined) {
        item.page_index = block.page_index;
      }
      if (block.start_ms !== undefined) {
        item.start_ms = block.start_ms;
      }
      if (block.end_ms !== undefined) {
        item.end_ms = block.end_ms;
      }
      if (block.text) {
        item.text = block.text;
      }
      return item;
    }
    default:
      return block.text || block.raw ? { type: output ? 'output_text' : 'input_text', text: block.text ?? block.raw ?? '' } : null;
  }
}

function anthropicContentBlock(block: ContentBlock): Record<string, unknown> | null {
  switch (block.type) {
    case 'text':
      return { type: 'text', text: block.text ?? block.raw ?? '' };
    case 'image': {
      const item: Record<string, unknown> = { type: 'image' };
      if (block.source_url) {
        item.source_url = block.source_url;
      }
      if (block.provider_file_id) {
        item.file_id = block.provider_file_id;
      }
      if (block.mime_type) {
        item.mime_type = block.mime_type;
      }
      if (block.blob_id) {
        item.blob_id = block.blob_id;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.text) {
        item.text = block.text;
      }
      return item;
    }
    case 'audio': {
      const item: Record<string, unknown> = { type: 'audio' };
      if (block.source_url) {
        item.source_url = block.source_url;
      }
      if (block.provider_file_id) {
        item.file_id = block.provider_file_id;
      }
      if (block.mime_type) {
        item.mime_type = block.mime_type;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.text) {
        item.text = block.text;
      }
      if (block.start_ms !== undefined) {
        item.start_ms = block.start_ms;
      }
      if (block.end_ms !== undefined) {
        item.end_ms = block.end_ms;
      }
      return item;
    }
    case 'video': {
      const item: Record<string, unknown> = { type: 'video' };
      if (block.source_url) {
        item.source_url = block.source_url;
      }
      if (block.provider_file_id) {
        item.file_id = block.provider_file_id;
      }
      if (block.mime_type) {
        item.mime_type = block.mime_type;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.text) {
        item.text = block.text;
      }
      if (block.start_ms !== undefined) {
        item.start_ms = block.start_ms;
      }
      if (block.end_ms !== undefined) {
        item.end_ms = block.end_ms;
      }
      return item;
    }
    case 'file': {
      const item: Record<string, unknown> = { type: 'document' };
      if (block.source_url) {
        item.source_url = block.source_url;
      }
      if (block.provider_file_id) {
        item.file_id = block.provider_file_id;
      }
      if (block.mime_type) {
        item.mime_type = block.mime_type;
      }
      if (block.filename) {
        item.filename = block.filename;
      }
      if (block.page_index !== undefined) {
        item.page_index = block.page_index;
      }
      if (block.text) {
        item.text = block.text;
      }
      return item;
    }
    default:
      return block.text || block.raw ? { type: 'text', text: block.text ?? block.raw ?? '' } : null;
  }
}

function gatewayBlocks(value: unknown): ContentBlock[] {
  if (typeof value === 'string') {
    return value.trim() ? [{ type: 'text', text: value }] : [];
  }
  if (Array.isArray(value)) {
    return value.flatMap((item) => gatewayBlocks(item));
  }
  const record = asRecord(value);
  if (Object.keys(record).length === 0) {
    return [];
  }

  const type = String(record.type ?? '').toLowerCase();
  switch (type) {
    case 'text':
    case 'input_text':
    case 'output_text':
      return [{ type: 'text', text: String(record.text ?? record.output_text ?? '') }];
    case 'image':
    case 'image_url':
    case 'input_image': {
      const image = asRecord(record.image_url ?? record.image);
      return [{
        type: 'image',
        source_url: stringOrUndefined(image.url) ?? stringOrUndefined(image.image_url) ?? stringOrUndefined(record.source_url) ?? stringOrUndefined(record.image_url),
        provider_file_id: stringOrUndefined(record.file_id) ?? stringOrUndefined(image.file_id) ?? stringOrUndefined(record.provider_file_id),
        blob_id: stringOrUndefined(record.blob_id) ?? stringOrUndefined(image.blob_id),
        mime_type: stringOrUndefined(record.mime_type) ?? stringOrUndefined(image.mime_type),
        filename: stringOrUndefined(record.filename),
        text: stringOrUndefined(record.text)
      }];
    }
    case 'audio':
    case 'input_audio': {
      const audio = asRecord(record.audio);
      return [{
        type: 'audio',
        source_url: stringOrUndefined(audio.url) ?? stringOrUndefined(audio.source_url) ?? stringOrUndefined(record.source_url),
        provider_file_id: stringOrUndefined(audio.file_id) ?? stringOrUndefined(record.file_id),
        blob_id: stringOrUndefined(record.blob_id),
        mime_type: stringOrUndefined(audio.mime_type) ?? stringOrUndefined(record.mime_type),
        filename: stringOrUndefined(record.filename),
        text: stringOrUndefined(record.text),
        start_ms: numberOrUndefined(record.start_ms),
        end_ms: numberOrUndefined(record.end_ms),
        duration_ms: numberOrUndefined(record.duration_ms)
      }];
    }
    case 'video':
    case 'input_video': {
      const video = asRecord(record.video);
      return [{
        type: 'video',
        source_url: stringOrUndefined(video.url) ?? stringOrUndefined(video.source_url) ?? stringOrUndefined(record.source_url),
        provider_file_id: stringOrUndefined(video.file_id) ?? stringOrUndefined(record.file_id),
        blob_id: stringOrUndefined(record.blob_id),
        mime_type: stringOrUndefined(video.mime_type) ?? stringOrUndefined(record.mime_type),
        filename: stringOrUndefined(record.filename),
        text: stringOrUndefined(record.text),
        start_ms: numberOrUndefined(record.start_ms),
        end_ms: numberOrUndefined(record.end_ms),
        duration_ms: numberOrUndefined(record.duration_ms)
      }];
    }
    case 'file':
    case 'input_file':
    case 'document':
      return [{
        type: 'file',
        source_url: stringOrUndefined(record.source_url),
        provider_file_id: stringOrUndefined(record.file_id) ?? stringOrUndefined(record.provider_file_id),
        blob_id: stringOrUndefined(record.blob_id),
        mime_type: stringOrUndefined(record.mime_type),
        filename: stringOrUndefined(record.filename),
        text: stringOrUndefined(record.text),
        page_index: numberOrUndefined(record.page_index),
        start_ms: numberOrUndefined(record.start_ms),
        end_ms: numberOrUndefined(record.end_ms)
      }];
    default:
      if (typeof record.content === 'string' || Array.isArray(record.content) || (record.content && typeof record.content === 'object')) {
        return gatewayBlocks(record.content);
      }
      return typeof record.text === 'string' && record.text.trim() ? [{ type: 'text', text: record.text }] : [];
  }
}

function blocksText(blocks: ContentBlock[]): string {
  return blocks
    .map((block) => block.text ?? block.raw ?? '')
    .filter((value) => typeof value === 'string' && value.trim().length > 0)
    .join('');
}

function isThinkingType(value: unknown): boolean {
  const text = String(value ?? '').toLowerCase();
  return text.includes('thinking') || text.includes('reasoning');
}

function contentText(value: unknown): string {
  if (typeof value === 'string') {
    return value;
  }
  if (Array.isArray(value)) {
    return value
      .map((item) => {
        const record = asRecord(item);
        if (isThinkingType(record.type)) {
          return '';
        }
        if (record.type === 'output_text' || record.type === 'input_text' || record.type === 'text') {
          return String(record.text ?? '');
        }
        return contentText(record.content ?? record.text);
      })
      .join('');
  }
  const record = asRecord(value);
  if (record.type === 'text' || record.type === 'output_text' || record.type === 'input_text') {
    return String(record.text ?? '');
  }
  if (typeof record.content === 'string') {
    return record.content;
  }
  return '';
}

function thinkingText(value: unknown): string {
  if (typeof value === 'string') {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((item) => thinkingText(item)).join('');
  }
  const record = asRecord(value);
  if (isThinkingType(record.type)) {
    return String(record.text ?? record.delta ?? record.thinking ?? '');
  }
  if (record.summary) {
    return thinkingText(record.summary);
  }
  if (record.content) {
    return thinkingText(record.content);
  }
  if (record.reasoning) {
    return thinkingText(record.reasoning);
  }
  if (record.thinking) {
    return thinkingText(record.thinking);
  }
  if (record.text && isThinkingType(record.kind ?? record.label ?? record.role)) {
    return String(record.text);
  }
  return '';
}
