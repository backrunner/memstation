import type {
  AdminConfigPayload,
  AdminSessionState,
  APIKeyRecord,
  AuditEventRecord,
  CacheStatusPayload,
  ChatThreadMessage,
  ConversationListItem,
  ConversationMemoryPayload,
  ConversationRecord,
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
  MemoryPreviewPayload,
  ProviderDiagnostic,
  QueueInfo,
  QueueTaskDetailPayload,
  QueueTaskRecord,
  SearchMemoriesResult,
  TokenStatsResponse,
  ToolCall,
  UserDetailPayload,
  UserSummary,
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
  return requestJSON<T>(`${normalizeBaseUrl(target.baseUrl)}${path}`, {
    credentials: 'include',
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {})
    }
  });
}

export async function loginAdmin(target: MemStationTarget, token: string): Promise<void> {
  await adminRequest(target, '/api/admin/login', {
    method: 'POST',
    body: JSON.stringify({ token })
  });
}

export async function logoutAdmin(target: MemStationTarget): Promise<void> {
  await adminRequest(target, '/api/admin/logout', {
    method: 'POST',
    body: JSON.stringify({})
  });
}

export async function fetchAdminSession(target: MemStationTarget): Promise<AdminSessionState> {
  try {
    const payload = await adminRequest<{ authenticated: boolean }>(target, '/api/admin/session', {
      method: 'GET'
    });
    return {
      authenticated: payload.authenticated,
      checking: false,
      checkedAt: new Date().toISOString()
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
  userId?: string
): Promise<ConversationRecord> {
  const query = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  const payload = await adminRequest<ConversationRecord>(target, `/api/admin/conversations/${conversationId}${query}`);
  return {
    ...payload,
    messages: (payload.messages ?? []).map((message) => normalizeMessage(message as unknown as Record<string, unknown>)),
    segments: Array.isArray(payload.segments)
      ? payload.segments.map((segment) => normalizeConversationSegment(segment as unknown as Record<string, unknown>))
      : undefined
  };
}

export function fetchConversationMemory(
  target: MemStationTarget,
  conversationId: string,
  userId?: string
): Promise<ConversationMemoryPayload> {
  const query = userId?.trim() ? `?user_id=${encodeURIComponent(userId.trim())}` : '';
  return adminRequest<{
    conversation_id: string;
    documents?: Array<Record<string, unknown>>;
    key_memories?: Array<Record<string, unknown>>;
  }>(target, `/api/admin/conversations/${conversationId}/memory${query}`).then((payload) => ({
    conversation_id: payload.conversation_id,
    documents: (payload.documents ?? []).map((item) => normalizeMemoryDocument(item)),
    key_memories: (payload.key_memories ?? []).map((item) => normalizeKeyMemoryRecord(item))
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

export function fetchHealth(target: MemStationTarget, path: '/health/live' | '/health/ready'): Promise<HealthPayload> {
  return requestJSON<HealthPayload>(`${normalizeBaseUrl(target.baseUrl)}${path}`, {
    credentials: 'include'
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
  options?: { kind?: 'summary' | 'impression' | 'fact'; userId?: string; limit?: number }
): Promise<MemoryDocument[]> {
  const query = new URLSearchParams();
  if (options?.kind) {
    query.set('kind', options.kind);
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
  kind: 'summary' | 'impression' | 'fact',
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
  kind: 'summary' | 'impression' | 'fact',
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
    memoryTypes?: Array<'summary' | 'impression' | 'fact' | 'key_memory'>;
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
): Promise<Record<string, unknown>> {
  return adminRequest(target, `/api/admin/users/${encodeURIComponent(userId)}/settings`, {
    method: 'PUT',
    body: JSON.stringify(payload)
  });
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
        content: message.text
      };
    }
    const payload: Record<string, unknown> = {
      role: message.role,
      content: message.text
    };
    if (message.toolCalls?.length) {
      payload.tool_calls = message.toolCalls.map((call) => ({
        id: call.id,
        type: 'function',
        function: {
          name: call.name,
          arguments: JSON.stringify(call.arguments ?? {})
        }
      }));
    }
    return payload;
  });
}

function responsesInput(messages: ChatThreadMessage[]): Array<Record<string, unknown>> {
  const items: Array<Record<string, unknown>> = [];
  for (const message of messages) {
    if (message.role === 'tool') {
      const toolResult = message.toolResults?.[0];
      items.push({
        type: 'function_call_output',
        call_id: toolResult?.tool_call_id ?? '',
        output: message.text
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
          arguments: JSON.stringify(call.arguments ?? {})
        });
      }
      if (!message.text.trim()) {
        continue;
      }
    }
    items.push({
      type: 'message',
      role: message.role,
      content: [{ type: 'input_text', text: message.text }]
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
            content: message.text
          }
        ]
      };
    }

    const content: Array<Record<string, unknown>> = [];
    if (message.text.trim()) {
      content.push({ type: 'text', text: message.text });
    }
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
      content: content.length === 1 && content[0]?.type === 'text' ? message.text : content
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
    memoryPersist: headers.get('X-MemStation-Memory-Persist') !== 'false'
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
  return {
    id: String(payload.id ?? makeId('chat')),
    role: 'assistant',
    text: contentText(message.content),
    thinking: thinkingText(message.reasoning ?? message.thinking),
    toolCalls: openAIToolCalls(message.tool_calls),
    usage: normalizeUsage(asRecord(payload.usage))
  };
}

function parseResponsesPayload(payload: Record<string, unknown>): ChatThreadMessage {
  const output = asArray(payload.output);
  const toolCalls: ToolCall[] = [];
  let text = '';
  let thinking = '';
  for (const item of output) {
    const record = asRecord(item);
    if (record.type === 'function_call') {
      toolCalls.push({
        id: String(record.call_id ?? record.id ?? makeId('tool-call')),
        name: String(record.name ?? ''),
        arguments: safeJSON(String(record.arguments ?? '{}'))
      });
      continue;
    }
    if (isThinkingType(record.type)) {
      thinking += thinkingText(record);
      continue;
    }
    if (record.type === 'message') {
      text += contentText(record.content);
      thinking += thinkingText(record.content);
    }
  }
  return {
    id: String(payload.id ?? makeId('response')),
    role: 'assistant',
    text,
    thinking,
    toolCalls,
    usage: normalizeUsage(asRecord(payload.usage))
  };
}

function parseAnthropicPayload(payload: Record<string, unknown>): ChatThreadMessage {
  const content = asArray(payload.content);
  const toolCalls: ToolCall[] = [];
  let text = '';
  let thinking = '';
  for (const item of content) {
    const record = asRecord(item);
    if (record.type === 'tool_use') {
      toolCalls.push({
        id: String(record.id ?? makeId('anthropic-tool')),
        name: String(record.name ?? ''),
        arguments: asRecord(record.input)
      });
      continue;
    }
    if (record.type === 'thinking' || record.type === 'redacted_thinking') {
      thinking += thinkingText(record);
      continue;
    }
    text += contentText(record);
  }
  return {
    id: String(payload.id ?? makeId('anthropic')),
    role: 'assistant',
    text,
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
    const parsed = safeJSON(data);
    if (!parsed || Array.isArray(parsed)) {
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

function normalizeMessage(payload: Record<string, unknown>): ChatThreadMessage {
  return {
    id: String(payload.id ?? makeId('message')),
    role: String(payload.role ?? 'assistant') as ChatThreadMessage['role'],
    text: String(payload.text ?? ''),
    blocks: Array.isArray(payload.blocks)
      ? (payload.blocks as Array<{ type: string; text?: string; raw?: string }>)
      : undefined,
    toolCalls: asArray(payload.tool_calls).map((item) => {
      const record = asRecord(item);
      return {
        id: String(record.id ?? makeId('tool-call')),
        name: String(record.name ?? ''),
        arguments: asRecord(record.arguments)
      };
    }),
    toolResults: asArray(payload.tool_results).map((item) => {
      const record = asRecord(item);
      return {
        tool_call_id: String(record.tool_call_id ?? ''),
        name: typeof record.name === 'string' ? record.name : undefined,
        output: asRecord(record.output)
      };
    }),
    thinking: thinkingText(payload.thinking ?? payload.metadata),
    metadata: asRecord(payload.metadata)
  };
}

function normalizeMemoryDocument(payload: Record<string, unknown>): MemoryDocument {
  return {
    id: String(payload.id ?? ''),
    node_id: stringOrUndefined(payload.node_id) ?? '',
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
    related: Array.isArray(payload.related)
      ? payload.related
          .map((value) => value as Record<string, unknown>)
          .filter((value) => typeof value.id === 'string')
          .map((value) => ({
            id: String(value.id),
            type: String(value.type ?? ''),
            weight: Number(value.weight ?? 0)
          }))
      : undefined,
    created_at: stringOrUndefined(payload.created_at),
    updated_at: stringOrUndefined(payload.updated_at)
  };
}

function normalizeConversationSegment(payload: Record<string, unknown>): ConversationSegment {
  return {
    id: String(payload.id ?? ''),
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
    related: item.related
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

function openAIToolCalls(raw: unknown): ToolCall[] {
  return asArray(raw).map((item) => {
    const record = asRecord(item);
    const fn = asRecord(record.function);
    return {
      id: String(record.id ?? makeId('tool-call')),
      name: String(fn.name ?? ''),
      arguments: safeJSON(String(fn.arguments ?? '{}'))
    };
  });
}

function toUnifiedMessage(message: ChatThreadMessage): Record<string, unknown> {
  const result: Record<string, unknown> = {
    role: message.role,
    text: message.text
  };
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

function safeJSON(value: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed;
    }
    return {};
  } catch {
    return {};
  }
}

function appendChunk(current: string | undefined, chunk: string): string {
  if (!chunk) {
    return current ?? '';
  }
  return `${current ?? ''}${chunk}`;
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
