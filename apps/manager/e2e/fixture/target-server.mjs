import http from 'node:http';
import { argon2Sync, randomUUID, timingSafeEqual } from 'node:crypto';

const host = '127.0.0.1';
const port = Number(process.env.PORT || '18080');
const managerOrigin = process.env.MANAGER_ORIGIN || 'http://127.0.0.1:4173';
const adminToken = process.env.MEMSTATION_ADMIN_TOKEN || 'memstation-e2e-admin-token';
const gatewayAPIKey = process.env.MEMSTATION_GATEWAY_API_KEY || 'ms-e2e';
const sessionCookieName = 'memstation_admin_session';
const sessionCookieValue = 'fixture-session';
const gatewayExposeHeaders = 'X-MemStation-Conversation-ID, X-MemStation-Request-ID, X-MemStation-Memory-Persist';

const runtimeDefaults = Object.freeze({
  default_provider: 'openai_compatible',
  internal_default_provider: 'openai_compatible',
  vector_store_backend: 'pgvector',
  conversation_timeout_seconds: 900,
  provider_timeout_seconds: 60,
  message_threshold: 32,
  token_threshold: 64000,
  max_context_tokens: 8192,
  max_injection_tokens: 2048,
  rerank_model: 'cohere:rerank-v3.5',
  rerank_candidate_multiplier: 6,
  rerank_min_candidates: 20,
  rerank_max_candidates: 50,
  rerank_blend_weight: 0.35,
  rate_limit_rps: 20,
  rate_limit_burst: 40,
  cache_ttl_seconds: 120,
  cors_allowed_origins: [managerOrigin],
  session_cookie_cross_site: false
});

const runtimeMeta = Object.freeze({
  default_provider: { apply_mode: 'live', source: 'default', category: 'traffic', description: 'Default gateway provider for automatic routing.' },
  internal_default_provider: { apply_mode: 'live', source: 'default', category: 'traffic', description: 'Default internal provider for summary and analysis workloads.' },
  vector_store_backend: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Vector store backend selection.' },
  conversation_timeout_seconds: { apply_mode: 'live', source: 'default', category: 'traffic', description: 'Conversation idle timeout.' },
  provider_timeout_seconds: { apply_mode: 'live', source: 'default', category: 'traffic', description: 'Per-provider upstream timeout.' },
  message_threshold: { apply_mode: 'live', source: 'default', category: 'traffic', description: 'Message count threshold for finalize.' },
  token_threshold: { apply_mode: 'live', source: 'default', category: 'traffic', description: 'Token threshold for finalize.' },
  max_context_tokens: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Injection context budget.' },
  max_injection_tokens: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Max injected memory tokens.' },
  rerank_model: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Provider-prefixed rerank model reference.' },
  rerank_candidate_multiplier: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Vector expansion factor before reranking.' },
  rerank_min_candidates: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Minimum candidate pool sent into reranking.' },
  rerank_max_candidates: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Maximum candidate pool sent into reranking.' },
  rerank_blend_weight: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Blend factor applied to rerank scores.' },
  rate_limit_rps: { apply_mode: 'live', source: 'default', category: 'web', description: 'Gateway rate limit per second.' },
  rate_limit_burst: { apply_mode: 'live', source: 'default', category: 'web', description: 'Gateway burst allowance.' },
  cache_ttl_seconds: { apply_mode: 'live', source: 'default', category: 'memory', description: 'Cache TTL in seconds.' },
  cors_allowed_origins: { apply_mode: 'live', source: 'default', category: 'web', description: 'Allowed manager origins.' },
  session_cookie_cross_site: { apply_mode: 'live', source: 'default', category: 'web', description: 'Cross-site cookie mode.' }
});

const providerSetDefaults = Object.freeze({
  gateway: [
    {
      provider: 'openai_compatible',
      base_url: 'https://api.openai.com/v1',
      host: 'api.openai.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    },
    {
      provider: 'openai_responses',
      base_url: 'https://api.openai.com/v1',
      host: 'api.openai.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    },
    {
      provider: 'anthropic',
      base_url: 'https://api.anthropic.com/v1',
      host: 'api.anthropic.com',
      scheme: 'https',
      has_api_key: false,
      source: 'fixture'
    },
    {
      provider: 'gemini',
      base_url: 'https://generativelanguage.googleapis.com/v1beta',
      host: 'generativelanguage.googleapis.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    }
  ],
  internal: [
    {
      provider: 'openai_compatible',
      base_url: 'https://api.openai.com/v1',
      host: 'api.openai.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    },
    {
      provider: 'openai_responses',
      base_url: 'https://api.openai.com/v1',
      host: 'api.openai.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    },
    {
      provider: 'anthropic',
      base_url: 'https://api.anthropic.com/v1',
      host: 'api.anthropic.com',
      scheme: 'https',
      has_api_key: false,
      source: 'fixture'
    },
    {
      provider: 'gemini',
      base_url: 'https://generativelanguage.googleapis.com/v1beta',
      host: 'generativelanguage.googleapis.com',
      scheme: 'https',
      has_api_key: false,
      source: 'fixture'
    }
  ],
  embedding: [
    {
      provider: 'openai_compatible',
      base_url: 'https://api.openai.com/v1',
      host: 'api.openai.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    }
  ],
  rerank: [
    {
      provider: 'cohere',
      base_url: 'https://api.cohere.com/v2',
      host: 'api.cohere.com',
      scheme: 'https',
      has_api_key: true,
      source: 'fixture'
    },
    {
      provider: 'jina',
      base_url: 'https://api.jina.ai/v1',
      host: 'api.jina.ai',
      scheme: 'https',
      has_api_key: false,
      source: 'fixture'
    }
  ]
});

const modelDefaults = Object.freeze({
  chat_model: 'gpt-4.1-mini',
  summary_model: 'gpt-4.1-mini',
  analysis_model: 'claude-3-7-sonnet',
  embedding_model: 'text-embedding-3-large',
  tool_write_enabled: false,
  max_injection_tokens: 2048
});

const databaseDefault = Object.freeze({
  configured: true,
  source: 'env',
  status: 'ready',
  display: 'postgres://fixture:****@127.0.0.1:5432/memstation?sslmode=disable',
  updated_at: isoOffset(180)
});

let state = createState();

function clone(value) {
  return structuredClone(value);
}

function nowISO() {
  return new Date().toISOString();
}

function isoOffset(minutes) {
  return new Date(Date.now() - minutes * 60 * 1000).toISOString();
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function json(req, res, payload, status = 200, extraHeaders = {}) {
  setCORSHeaders(req, res);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    ...extraHeaders
  });
  res.end(JSON.stringify(payload));
}

function noContent(req, res, status = 204, extraHeaders = {}) {
  setCORSHeaders(req, res);
  res.writeHead(status, extraHeaders);
  res.end();
}

function setCORSHeaders(req, res) {
  const origin = req.headers.origin;
  if (origin && origin === managerOrigin) {
    res.setHeader('Access-Control-Allow-Origin', origin);
    res.setHeader('Access-Control-Allow-Credentials', 'true');
    res.setHeader('Vary', 'Origin');
  }
}

async function readJSON(req) {
  const chunks = [];
  for await (const chunk of req) {
    chunks.push(chunk);
  }
  const raw = Buffer.concat(chunks).toString('utf8');
  return raw ? JSON.parse(raw) : {};
}

function cookies(req) {
  const header = req.headers.cookie || '';
  return Object.fromEntries(
    header
      .split(';')
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => {
        const index = part.indexOf('=');
        if (index === -1) {
          return [part, ''];
        }
        return [part.slice(0, index), decodeURIComponent(part.slice(index + 1))];
      })
  );
}

function isAdmin(req) {
  return cookies(req)[sessionCookieName] === sessionCookieValue;
}

function verifyAdminTokenHash(token, credential) {
  const prefix = '$memstation-admin-argon2id$';
  if (!credential.startsWith(prefix)) {
    return false;
  }
  const parts = credential.slice(prefix.length).split('$');
  if (parts.length !== 4 || parts[0] !== 'v=19') {
    return false;
  }

  const params = Object.fromEntries(
    parts[1].split(',').map((part) => {
      const [key, value] = part.split('=');
      return [key, Number(value)];
    })
  );
  const salt = Buffer.from(parts[2], 'base64');
  const expected = Buffer.from(parts[3], 'base64');
  if (!params.m || !params.t || !params.p || !salt.length || !expected.length) {
    return false;
  }

  try {
    const actual = argon2Sync('argon2id', {
      message: Buffer.from(token),
      nonce: salt,
      parallelism: params.p,
      tagLength: expected.length,
      memory: params.m,
      passes: params.t
    });
    return actual.length === expected.length && timingSafeEqual(actual, expected);
  } catch {
    return false;
  }
}

function ensureAdmin(req, res) {
  if (isAdmin(req)) {
    return true;
  }
  json(req, res, { error: { message: 'admin authentication required' } }, 401);
  return false;
}

function ensureGatewayAuth(req, res) {
  const auth = req.headers.authorization || '';
  if (auth === `Bearer ${gatewayAPIKey}`) {
    return true;
  }
  json(req, res, { error: { message: 'unauthorized' } }, 401);
  return false;
}

function userSummaries() {
  return state.users.map((user) => {
    const conversations = conversationList(user.user_id);
    const keyMemories = state.key_memories.filter((item) => item.user_id === user.user_id);
    const totalTokens = conversations.reduce((sum, item) => sum + item.total_tokens, 0);
    const recentActiveAt = conversations[0]?.updated_at;
    return {
      user_id: user.user_id,
      name: user.name,
      is_admin: user.is_admin,
      api_key_count: (state.api_keys[user.user_id] || []).filter((item) => !item.revoked_at).length,
      conversation_count: conversations.length,
      key_memory_count: keyMemories.length,
      total_tokens: totalTokens,
      recent_active_at: recentActiveAt,
      created_at: user.created_at
    };
  });
}

function conversationList(userId) {
  return Object.values(state.conversations)
    .filter((item) => !userId || item.user_id === userId)
    .sort((left, right) => right.updated_at.localeCompare(left.updated_at))
    .map((item) => ({
      conversation_id: item.conversation_id,
      user_id: item.user_id,
      provider: item.provider,
      protocol: item.request_format,
      mode: item.mode,
      finalize_status: item.finalize_status,
      summary_status: item.summary_status,
      analysis_status: item.analysis_status,
      message_count: item.messages.length,
      total_tokens: Number(item.usage.total_tokens || item.usage.totalTokens || 0),
      updated_at: item.updated_at,
      metadata: item.metadata || {}
    }));
}

function createConversationRecord({
  conversationId,
  userId,
  messages,
  parts = [],
  artifacts = [],
  segments = [],
  metadata = {},
  finalizeStatus = 'ended',
  summaryStatus = 'completed',
  analysisStatus = 'completed',
  updatedAt = nowISO(),
  mode = 'inject',
  provider = 'openai_compatible',
  requestFormat = 'chat_completions',
  usage = { input_tokens: 24, output_tokens: 18, total_tokens: 42 }
}) {
  return {
    conversation_id: conversationId,
    user_id: userId,
    request_format: requestFormat,
    provider,
    mode,
    finalize_status: finalizeStatus,
    summary_status: summaryStatus,
    analysis_status: analysisStatus,
    messages,
    parts,
    artifacts,
    segments,
    usage,
    metadata,
    created_at: updatedAt,
    updated_at: updatedAt
  };
}

function buildConversationMemory(conversationId) {
  return state.conversation_memories[conversationId] || {
    conversation_id: conversationId,
    documents: [],
    key_memories: []
  };
}

function findDocument(kind, id) {
  return (state.memory_documents[kind] || []).find((item) => item.id === id) || null;
}

function findKeyMemory(id, userId = '') {
  return state.key_memories.find((item) => item.id === id && (!userId || item.user_id === userId)) || null;
}

function filteredAudit(searchParams) {
  const limit = Number(searchParams.get('limit') || '100');
  const targetType = searchParams.get('target_type');
  const targetId = searchParams.get('target_id');
  const action = searchParams.get('action');
  const actorType = searchParams.get('actor_type');
  return state.audits
    .filter((item) => (!targetType || item.target_type === targetType))
    .filter((item) => (!targetId || item.target_id === targetId))
    .filter((item) => (!action || item.action === action))
    .filter((item) => (!actorType || item.actor_type === actorType))
    .slice(0, limit);
}

function addAudit(action, targetType, targetId, payload, actorType = 'admin', actorId = 'fixture-admin') {
  const event = {
    id: `audit-${randomUUID()}`,
    actor_type: actorType,
    actor_id: actorId,
    action,
    target_type: targetType,
    target_id: targetId,
    payload,
    created_at: nowISO()
  };
  state.audits.unshift(event);
  return event;
}

function tokenStats() {
  return userSummaries().map((user) => ({
    user_id: user.user_id,
    input_tokens: conversationList(user.user_id).reduce((sum, item) => sum + usageForConversation(item.conversation_id).input_tokens, 0),
    output_tokens: conversationList(user.user_id).reduce((sum, item) => sum + usageForConversation(item.conversation_id).output_tokens, 0),
    total_tokens: conversationList(user.user_id).reduce((sum, item) => sum + usageForConversation(item.conversation_id).total_tokens, 0)
  }));
}

function usageForConversation(conversationId) {
  const usage = state.conversations[conversationId]?.usage || {};
  return {
    input_tokens: Number(usage.input_tokens || usage.inputTokens || 0),
    output_tokens: Number(usage.output_tokens || usage.outputTokens || 0),
    total_tokens: Number(usage.total_tokens || usage.totalTokens || 0)
  };
}

function providerSetSummaries() {
  return Object.fromEntries(
    Object.entries(state.provider_sets).map(([scope, providers]) => [
      scope,
      providers.map((provider) => ({
        provider: provider.provider,
        base_url: provider.base_url,
        has_api_key: provider.has_api_key,
        source: provider.source,
        updated_at: provider.updated_at
      }))
    ])
  );
}

function runtimeDefaultFieldForScope(scope) {
  switch (scope) {
    case 'gateway':
      return 'default_provider';
    case 'internal':
      return 'internal_default_provider';
    default:
      return null;
  }
}

function buildRuntimeMeta() {
  return Object.fromEntries(
    Object.entries(state.runtime_meta).map(([key, meta]) => [
      key,
      {
        ...meta,
        source: state.runtime_override_keys[key] ? 'override' : 'default'
      }
    ])
  );
}

function buildConfigPayload() {
  const providerSets = providerSetSummaries();
  return {
    providers: providerSets.gateway || [],
    provider_sets: providerSets,
    database: clone(state.database),
    runtime_defaults: clone(state.runtime_defaults),
    runtime: clone(state.runtime),
    runtime_live: clone(state.runtime_live),
    runtime_meta: buildRuntimeMeta(),
    ...clone(state.model_settings)
  };
}

function buildProviderDiagnostics() {
  return Object.entries(state.provider_sets).flatMap(([scope, providers]) =>
    providers.map((provider) => {
      const defaultField = runtimeDefaultFieldForScope(scope);
      const isDesiredDefault = Boolean(defaultField && state.runtime[defaultField] === provider.provider);
      const isLiveDefault = Boolean(defaultField && state.runtime_live[defaultField] === provider.provider);
      const issues = [];
      if (!provider.base_url) {
        issues.push('Base URL missing in fixture runtime');
      }
      if (!provider.has_api_key) {
        issues.push(isDesiredDefault ? 'Default provider is missing an API key.' : 'API key missing in fixture runtime');
      }
      return {
        scope,
        provider: provider.provider,
        base_url: provider.base_url,
        host: provider.host,
        scheme: provider.scheme,
        has_api_key: provider.has_api_key,
        source: provider.source,
        updated_at: provider.updated_at,
        is_desired_default: isDesiredDefault,
        is_live_default: isLiveDefault,
        desired_timeout_seconds: state.runtime.provider_timeout_seconds,
        live_timeout_seconds: state.runtime_live.provider_timeout_seconds,
        restart_required: state.runtime.provider_timeout_seconds !== state.runtime_live.provider_timeout_seconds,
        status: issues.length === 0 ? 'ready' : isDesiredDefault ? 'degraded' : 'warning',
        issues
      };
    })
  );
}

function applyProviderOverride(scope, providerName, providerPatch) {
  const providers = state.provider_sets[scope];
  if (!Array.isArray(providers) || !providerPatch || typeof providerPatch !== 'object') {
    return;
  }
  const target = providers.find((item) => item.provider === providerName);
  if (!target) {
    return;
  }
  if (providerPatch.base_url) {
    target.base_url = String(providerPatch.base_url);
    try {
      const parsed = new URL(target.base_url);
      target.host = parsed.host;
      target.scheme = parsed.protocol.replace(/:$/, '');
    } catch {
      target.host = '';
      target.scheme = '';
    }
    target.source = 'override';
  }
  if (providerPatch.api_key) {
    target.has_api_key = true;
    target.source = 'override';
  }
  target.updated_at = nowISO();
}

function maskDatabaseURL(value) {
  const raw = String(value || '').trim();
  if (!raw) {
    return '';
  }
  try {
    const parsed = new URL(raw);
    if (parsed.password) {
      parsed.password = '****';
    }
    return parsed.toString();
  } catch {
    return '[configured]';
  }
}

function databaseSnapshot(urlValue, source = 'override') {
  const trimmed = String(urlValue || '').trim();
  if (!trimmed) {
    return {
      configured: false,
      source: 'unset',
      status: 'inactive',
      display: '',
      updated_at: nowISO()
    };
  }
  return {
    configured: true,
    source,
    status: trimmed.includes('invalid') ? 'degraded' : 'ready',
    display: maskDatabaseURL(trimmed),
    last_error: trimmed.includes('invalid') ? 'fixture failed to connect to the requested database' : '',
    updated_at: nowISO()
  };
}

function createPreview(conversationId, userText) {
  return {
    conversation_id: conversationId,
    summary_status: 'preview',
    analysis_status: 'preview',
    keywords: ['fixture', 'memory', 'preview'],
    summary: {
      id: `preview-summary-${conversationId}`,
      type: 'summary',
      content: `Preview summary for: ${userText}`,
      topic_label: 'Fixture preview'
    },
    fact: {
      id: `preview-fact-${conversationId}`,
      type: 'fact',
      content: `Preview fact extracted from: ${userText}`,
      metadata: { confidence: 0.82 }
    },
    impression: {
      id: `preview-impression-${conversationId}`,
      type: 'impression',
      content: 'The operator is validating memory preview behavior.',
      metadata: { source: 'fixture' }
    },
    key_memories: [
      {
        key: 'validation_mode',
        value: 'preview',
        source: 'fixture',
        category: 'playground',
        status: 'candidate'
      }
    ],
    metadata: {
      message_count: 2
    }
  };
}

function createConversationMemory(conversationId, userText, userId = 'admin') {
  const ts = nowISO();
  const documents = [
    {
      id: `summary-${conversationId}`,
      node_id: `node-summary-${conversationId}`,
      user_id: userId,
      conversation_id: conversationId,
      type: 'summary',
      content: `Persisted summary for: ${userText}`,
      importance: 1.1,
      decay_factor: 0.97,
      metadata: { topic_label: 'Fixture persisted summary' },
      related: [],
      created_at: ts,
      updated_at: ts
    },
    {
      id: `fact-${conversationId}`,
      node_id: `node-fact-${conversationId}`,
      user_id: userId,
      conversation_id: conversationId,
      type: 'fact',
      content: `Persisted fact for: ${userText}`,
      importance: 1,
      decay_factor: 0.94,
      metadata: { confidence: 0.9 },
      related: [],
      created_at: ts,
      updated_at: ts
    }
  ];
  const keyMemories = [
    {
      id: `key-${conversationId}`,
      user_id: userId,
      key: 'last_validation_target',
      value: conversationId,
      category: 'playground',
      source: 'fixture',
      review_status: 'pending',
      updated_at: ts,
      metadata: { conversation_id: conversationId }
    }
  ];
  return {
    conversation_id: conversationId,
    documents,
    key_memories: keyMemories
  };
}

function buildConversationSnapshot(conversationId, userId = 'admin') {
  const conversation = state.conversations[conversationId];
  if (!conversation) {
    return null;
  }
  return {
    snapshot_id: `snapshot-${conversationId}`,
    user_id: userId,
    conversation_id: conversationId,
    snapshot_version: 1,
    source_hash: `hash-${conversationId}`,
    request_format: conversation.request_format,
    provider: conversation.provider,
    mode: conversation.mode,
    finalize_status: conversation.finalize_status,
    message_count: conversation.messages.length,
    metadata: {
      fixture: true
    },
    created_at: conversation.created_at,
    updated_at: conversation.updated_at
  };
}

function buildUserSources(userId) {
  const conversations = conversationList(userId);
  const latestSnapshot = conversations.length > 0 ? buildConversationSnapshot(conversations[0].conversation_id, userId) : null;
  const embeddingModel = String(state.model_settings.embedding_model || '');
  const activeGeneration = {
    generation_id: `generation-${userId}-active`,
    user_id: userId,
    status: 'completed',
    scope: 'user',
    is_active: true,
    structure_version: 1,
    embedding_version: 1,
    embedding_model: embeddingModel,
    activate_on_success: true,
    source_count: conversations.length,
    processed_count: conversations.length,
    embedding_count: conversations.length,
    failure_count: 0,
    metadata: {
      fixture: true
    },
    created_at: latestSnapshot?.created_at || nowISO(),
    updated_at: latestSnapshot?.updated_at || nowISO(),
    activated_at: latestSnapshot?.updated_at || nowISO()
  };
  return {
    user_id: userId,
    source_stats: {
      snapshot_count: conversations.length,
      latest_snapshot_at: latestSnapshot?.updated_at,
      latest_snapshot_user_id: latestSnapshot?.user_id
    },
    active_generation: activeGeneration,
    generations: [activeGeneration]
  };
}

function buildConversationSources(conversationId, userId = 'admin') {
  const latestSnapshot = buildConversationSnapshot(conversationId, userId);
  return {
    user_id: userId,
    conversation_id: conversationId,
    has_snapshot: Boolean(latestSnapshot),
    latest_snapshot: latestSnapshot || undefined,
    snapshots: latestSnapshot ? [latestSnapshot] : []
  };
}

function allSearchableMemories(userId) {
  const docs = Object.values(state.memory_documents)
    .flat()
    .filter((item) => !userId || item.user_id === userId)
    .map((item) => ({
      id: item.id,
      type: item.type,
      content: item.content,
      score: 0,
      effectiveScore: 0,
      metadata: item.metadata || {}
    }));
  const keys = state.key_memories
    .filter((item) => !userId || item.user_id === userId)
    .map((item) => ({
      id: item.id,
      type: 'key_memory',
      content: `${item.key}: ${item.value}`,
      score: 0,
      effectiveScore: 0,
      metadata: item.metadata || {}
    }));
  return [...docs, ...keys];
}

function searchMemories(query, topN = 10, memoryTypes = [], minScore = 0, userId) {
  const tokens = tokensFromQuery(query);
  const tokenCount = Math.max(1, tokens.length);
  const filtered = allSearchableMemories(userId)
    .filter((item) => memoryTypes.length === 0 || memoryTypes.includes(item.type))
    .map((item) => {
      const haystack = `${item.content} ${JSON.stringify(item.metadata)}`.toLowerCase();
      const matches = tokens.filter((token) => haystack.includes(token)).length;
      const fallback = haystack.includes('memstation') ? 1 : 0;
      const score = Math.min(0.99, 0.45 + (matches + fallback) / Math.max(tokenCount, 1) * 0.4);
      const effectiveScore = Math.min(0.99, score * (item.type === 'summary' ? 0.98 : item.type === 'fact' ? 0.95 : 0.92));
      return {
        ...item,
        score,
        effectiveScore
      };
    })
    .filter((item) => item.score >= minScore)
    .sort((left, right) => right.effectiveScore - left.effectiveScore)
    .slice(0, topN);

  return {
    total: filtered.length,
    queryEmbeddingTokens: Math.max(8, tokenCount * 6),
    memories: filtered
  };
}

function tokensFromQuery(query) {
  return String(query || '')
    .trim()
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean);
}

function findQueue(name) {
  return state.queues.find((queue) => queue.name === name) || null;
}

function listTasks({ queue, states, limit = 20 }) {
  const stateSet = new Set(states || []);
  return state.tasks
    .filter((item) => !queue || item.queue === queue)
    .filter((item) => stateSet.size === 0 || stateSet.has(item.state))
    .slice(0, limit);
}

function findTask(queue, id) {
  return state.tasks.find((item) => item.queue === queue && item.id === id) || null;
}

function nextAPIKey(userId, name) {
  const id = `api-key-${state.next_api_key_id++}`;
  const prefix = `ms-${Math.random().toString(36).slice(2, 7)}`;
  const record = {
    id,
    user_id: userId,
    name,
    prefix,
    created_at: nowISO()
  };
  const plaintext = `${prefix}.${randomUUID().replace(/-/g, '')}`;
  return { record, plaintext };
}

function normalizeChatMessages(payload) {
  const messages = Array.isArray(payload.messages) ? payload.messages : [];
  return messages.map((item, index) => ({
    id: item.id || `input-${index}`,
    role: item.role || 'user',
    text: typeof item.content === 'string' ? item.content : typeof item.text === 'string' ? item.text : ''
  }));
}

function normalizeResponsesMessages(payload) {
  const input = Array.isArray(payload.input) ? payload.input : [];
  const messages = [];
  let index = 0;
  for (const item of input) {
    if (!item || typeof item !== 'object') {
      continue;
    }
    if (item.type === 'function_call_output') {
      messages.push({
        id: item.call_id || `tool-${index}`,
        role: 'tool',
        text: typeof item.output === 'string' ? item.output : JSON.stringify(item.output || {})
      });
      index += 1;
      continue;
    }
    if (item.type !== 'message') {
      continue;
    }
    const parts = Array.isArray(item.content) ? item.content : [];
    const textContent = parts
      .map((part) => {
        if (!part || typeof part !== 'object') {
          return '';
        }
        if (part.type === 'input_text' || part.type === 'output_text' || part.type === 'text') {
          return String(part.text || '');
        }
        return '';
      })
      .join('');
    messages.push({
      id: item.id || `input-${index}`,
      role: item.role || 'user',
      text: textContent
    });
    index += 1;
  }
  return messages;
}

function normalizeAnthropicMessages(payload) {
  const messages = Array.isArray(payload.messages) ? payload.messages : [];
  return messages.map((item, index) => ({
    id: item.id || `input-${index}`,
    role: item.role || 'user',
    text: anthropicContentText(item.content)
  }));
}

function anthropicContentText(value) {
  if (typeof value === 'string') {
    return value;
  }
  if (!Array.isArray(value)) {
    return '';
  }
  return value
    .map((item) => {
      if (!item || typeof item !== 'object') {
        return '';
      }
      if (item.type === 'text' || item.type === 'input_text' || item.type === 'output_text') {
        return String(item.text || '');
      }
      return '';
    })
    .join('');
}

function extractLastUserText(protocol, payload) {
  const messages = protocol === 'responses'
    ? normalizeResponsesMessages(payload)
    : protocol === 'anthropic_messages'
      ? normalizeAnthropicMessages(payload)
      : normalizeChatMessages(payload);
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const item = messages[index];
    if (item.role === 'user') {
      return item.text || 'fixture request';
    }
  }
  return 'fixture request';
}

function normalizeInboundMessages(protocol, payload) {
  switch (protocol) {
    case 'responses':
      return normalizeResponsesMessages(payload);
    case 'anthropic_messages':
      return normalizeAnthropicMessages(payload);
    case 'chat_completions':
    default:
      return normalizeChatMessages(payload);
  }
}

async function writeFrames(res, frames, chunkDelayMs = 0) {
  for (const frame of frames) {
    res.write(frame);
    if (chunkDelayMs > 0) {
      await sleep(chunkDelayMs);
    }
  }
}

async function writeChatCompletionStream(req, res, requestId, responseText, thinking, usage, chunkDelayMs = 0) {
  setCORSHeaders(req, res);
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'Access-Control-Allow-Origin': managerOrigin,
    'Access-Control-Allow-Credentials': 'true',
    'Access-Control-Expose-Headers': gatewayExposeHeaders
  });
  await writeFrames(
    res,
    [
      `data: ${JSON.stringify({ id: `chatcmpl-${requestId}`, choices: [{ delta: { role: 'assistant', reasoning: thinking } }] })}\n\n`,
      `data: ${JSON.stringify({ id: `chatcmpl-${requestId}`, choices: [{ delta: { content: responseText.slice(0, Math.ceil(responseText.length / 2)) } }] })}\n\n`,
      `data: ${JSON.stringify({
        id: `chatcmpl-${requestId}`,
        choices: [{ delta: { content: responseText.slice(Math.ceil(responseText.length / 2)) } }],
        usage: { prompt_tokens: usage.input_tokens, completion_tokens: usage.output_tokens, total_tokens: usage.total_tokens }
      })}\n\n`,
      'data: [DONE]\n\n'
    ],
    chunkDelayMs
  );
  res.end();
}

async function writeResponsesStream(req, res, responseText, thinking, chunkDelayMs = 0) {
  setCORSHeaders(req, res);
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'Access-Control-Allow-Origin': managerOrigin,
    'Access-Control-Allow-Credentials': 'true',
    'Access-Control-Expose-Headers': gatewayExposeHeaders
  });
  await writeFrames(
    res,
    [
      `event: response.reasoning.delta\ndata: ${JSON.stringify({ type: 'response.reasoning.delta', delta: thinking })}\n\n`,
      `event: response.output_text.delta\ndata: ${JSON.stringify({ type: 'response.output_text.delta', delta: responseText.slice(0, Math.ceil(responseText.length / 2)) })}\n\n`,
      `event: response.output_text.delta\ndata: ${JSON.stringify({ type: 'response.output_text.delta', delta: responseText.slice(Math.ceil(responseText.length / 2)) })}\n\n`,
      'data: [DONE]\n\n'
    ],
    chunkDelayMs
  );
  res.end();
}

async function writeAnthropicStream(req, res, responseText, thinking, chunkDelayMs = 0) {
  setCORSHeaders(req, res);
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'Access-Control-Allow-Origin': managerOrigin,
    'Access-Control-Allow-Credentials': 'true',
    'Access-Control-Expose-Headers': gatewayExposeHeaders
  });
  await writeFrames(
    res,
    [
      `event: content_block_start\ndata: ${JSON.stringify({ type: 'content_block_start', content_block: { type: 'thinking', text: thinking } })}\n\n`,
      `event: content_block_delta\ndata: ${JSON.stringify({ type: 'content_block_delta', delta: { type: 'text', text: responseText.slice(0, Math.ceil(responseText.length / 2)) } })}\n\n`,
      `event: content_block_delta\ndata: ${JSON.stringify({ type: 'content_block_delta', delta: { type: 'text', text: responseText.slice(Math.ceil(responseText.length / 2)) } })}\n\n`,
      'data: [DONE]\n\n'
    ],
    chunkDelayMs
  );
  res.end();
}

async function handleGatewayRequest(protocol, req, res, body) {
  const stream = body.stream !== false;
  const userText = extractLastUserText(protocol, body);
  const normalizedUserText = userText.toLowerCase();
  const conversationId = String(req.headers['x-memstation-session'] || `playground-${randomUUID()}`);
  const requestId = `req-${state.next_request_id++}`;
  const memoryPersist = req.headers['x-memstation-memory-persist'] !== 'false';
  const mode = typeof req.headers['x-memstation-mode'] === 'string' ? req.headers['x-memstation-mode'] : 'inject';
  const endConversation = req.headers['x-memstation-end-conversation'] === 'true';
  const provider = typeof req.headers['x-memstation-provider'] === 'string' && req.headers['x-memstation-provider']
    ? req.headers['x-memstation-provider']
    : state.runtime.default_provider;
  const requestFormat = protocol;
  const responseText = `Fixture stream response for: ${userText}`;
  const thinking = 'Reviewing retrieved fixture memory before composing the answer.';
  const usage = { input_tokens: 18, output_tokens: 14, total_tokens: 32 };
  const chunkDelayMs = normalizedUserText.includes('slow background completion') ? 650 : 0;
  const injection = {
    query: userText.toLowerCase(),
    total: 1,
    query_embedding_tokens: 9,
    items: [
      {
        id: 'summary-1',
        node_id: 'node-summary-1',
        type: 'summary',
        content: state.memory_documents.summary[0].content,
        score: 0.93,
        effective_score: 0.88,
        conversation_id: 'conv-seed-1',
        matched_keywords: ['gateway', 'memory'],
        keywords: state.memory_documents.summary[0].metadata?.keywords || []
      }
    ]
  };

  if (normalizedUserText.includes('recharge required')) {
    json(
      req,
      res,
      {
        error: {
          message: 'Please recharge your balance before continuing this conversation.'
        }
      },
      402,
      {
        'X-MemStation-Conversation-ID': conversationId,
        'X-MemStation-Request-ID': requestId,
        'X-MemStation-Memory-Persist': String(memoryPersist),
        'Access-Control-Expose-Headers': gatewayExposeHeaders
      }
    );
    return;
  }

  const assistantMessage = {
    id: `assistant-${requestId}`,
    role: 'assistant',
    text: responseText,
    thinking,
    usage
  };

  state.conversations[conversationId] = createConversationRecord({
    conversationId,
    userId: 'admin',
    messages: [...normalizeInboundMessages(protocol, body), assistantMessage],
    metadata: { injection },
    finalizeStatus: endConversation ? 'ending' : 'active',
    summaryStatus: memoryPersist ? 'completed' : 'preview',
    analysisStatus: memoryPersist ? 'completed' : 'preview',
    updatedAt: nowISO(),
    mode,
    provider,
    requestFormat,
    usage
  });

  if (memoryPersist) {
    const memory = createConversationMemory(conversationId, userText, 'admin');
    state.conversation_memories[conversationId] = memory;
    for (const document of memory.documents) {
      if (document.type === 'summary' || document.type === 'fact' || document.type === 'impression') {
        state.memory_documents[document.type].unshift(document);
      }
    }
    for (const item of memory.key_memories) {
      state.key_memories.unshift(item);
    }
    delete state.previews[conversationId];
  } else {
    state.previews[conversationId] = createPreview(conversationId, userText);
    delete state.conversation_memories[conversationId];
  }

  setCORSHeaders(req, res);
  res.setHeader('X-MemStation-Conversation-ID', conversationId);
  res.setHeader('X-MemStation-Request-ID', requestId);
  res.setHeader('X-MemStation-Memory-Persist', String(memoryPersist));
  res.setHeader('Access-Control-Expose-Headers', gatewayExposeHeaders);

  if (!stream) {
    if (protocol === 'responses') {
      json(req, res, {
        id: `resp-${requestId}`,
        object: 'response',
        output: [
          { type: 'reasoning', text: thinking },
          { type: 'message', role: 'assistant', content: [{ type: 'output_text', text: responseText }] }
        ],
        usage
      });
      return;
    }

    if (protocol === 'anthropic_messages') {
      json(req, res, {
        id: `msg-${requestId}`,
        type: 'message',
        role: 'assistant',
        content: [
          { type: 'thinking', text: thinking },
          { type: 'text', text: responseText }
        ],
        usage: {
          input_tokens: usage.input_tokens,
          output_tokens: usage.output_tokens,
          total_tokens: usage.total_tokens
        }
      });
      return;
    }

    json(req, res, {
      id: `chatcmpl-${requestId}`,
      object: 'chat.completion',
      created: Math.floor(Date.now() / 1000),
      model: body.model || state.model_settings.chat_model,
      choices: [
        {
          index: 0,
          message: {
            role: 'assistant',
            content: responseText,
            reasoning: thinking
          }
        }
      ],
      usage: {
        prompt_tokens: usage.input_tokens,
        completion_tokens: usage.output_tokens,
        total_tokens: usage.total_tokens
      }
    });
    return;
  }

  if (protocol === 'responses') {
    await writeResponsesStream(req, res, responseText, thinking, chunkDelayMs);
    return;
  }
  if (protocol === 'anthropic_messages') {
    await writeAnthropicStream(req, res, responseText, thinking, chunkDelayMs);
    return;
  }
  await writeChatCompletionStream(req, res, requestId, responseText, thinking, usage, chunkDelayMs);
}

function createState() {
  const createdAt = isoOffset(180);
  const secondConversationAt = isoOffset(90);
  const multimodalConversationAt = isoOffset(30);
  const now = isoOffset(5);

  const conversations = {
    'conv-seed-1': createConversationRecord({
      conversationId: 'conv-seed-1',
      userId: 'admin',
      messages: [
        { id: 'msg-seed-1', role: 'user', text: 'Summarize the MemStation launch checklist.' },
        {
          id: 'msg-seed-2',
          role: 'assistant',
          text: 'The launch checklist focuses on gateway rollout, memory verification, and admin observability.',
          thinking: 'Reviewing rollout notes and existing launch memory.',
          usage: { input_tokens: 20, output_tokens: 16, total_tokens: 36 }
        }
      ],
      metadata: {
        injection: {
          query: 'memstation launch checklist',
          total: 1,
          query_embedding_tokens: 10,
          items: [
            {
              id: 'summary-1',
              node_id: 'node-summary-1',
              type: 'summary',
              content: 'MemStation launch tracks gateway rollout, memory verification, and admin observability.',
              score: 0.93,
              effective_score: 0.88,
              conversation_id: 'conv-seed-1'
            }
          ]
        }
      },
      updatedAt: createdAt,
      usage: { input_tokens: 20, output_tokens: 16, total_tokens: 36 }
    }),
    'conv-seed-2': createConversationRecord({
      conversationId: 'conv-seed-2',
      userId: 'admin',
      messages: [
        { id: 'msg-seed-3', role: 'user', text: 'What provider should handle analysis-heavy prompts?' },
        {
          id: 'msg-seed-4',
          role: 'assistant',
          text: 'Anthropic is staged for deeper analysis tasks in the current fixture configuration.',
          thinking: 'Comparing configured provider roles for analysis and summaries.',
          usage: { input_tokens: 22, output_tokens: 18, total_tokens: 40 }
        }
      ],
      metadata: {
        injection: {
          query: 'provider analysis prompts',
          total: 1,
          query_embedding_tokens: 8,
          items: [
            {
              id: 'fact-1',
              node_id: 'node-fact-1',
              type: 'fact',
              content: 'Gateway smoke coverage is required before rollout.',
              score: 0.84,
              effective_score: 0.79,
              conversation_id: 'conv-seed-1'
            }
          ]
        }
      },
      updatedAt: secondConversationAt,
      provider: 'anthropic',
      requestFormat: 'anthropic_messages',
      usage: { input_tokens: 22, output_tokens: 18, total_tokens: 40 }
    }),
    'conv-mm-tool-1': createConversationRecord({
      conversationId: 'conv-mm-tool-1',
      userId: 'admin',
      messages: [
        {
          id: 'msg-mm-user-1',
          role: 'user',
          text: 'Inspect the screenshot and meeting clip from the agent tool payload.',
          blocks: [
            {
              type: 'text',
              text: 'Inspect the screenshot and meeting clip from the agent tool payload.'
            }
          ]
        },
        {
          id: 'msg-mm-assistant-1',
          role: 'assistant',
          text: 'Calling inspect_assets on the tool payload.',
          tool_calls: [
            {
              id: 'tool-mm-1',
              name: 'inspect_assets',
              arguments: {
                prompt: 'extract release evidence',
                attachments: [
                  {
                    type: 'input_image',
                    image_url: {
                      url: 'https://example.com/tool-diagram.png',
                      mime_type: 'image/png'
                    },
                    filename: 'diagram-tool.png',
                    text: 'diagram notes that Chen owns the rollback plan'
                  },
                  {
                    type: 'file',
                    source_url: 'https://example.com/tool-brief.pdf',
                    mime_type: 'application/pdf',
                    filename: 'tool-brief.pdf',
                    text: 'page 2 mentions Chen as the rollback owner'
                  }
                ]
              }
            }
          ]
        },
        {
          id: 'msg-mm-tool-1',
          role: 'tool',
          text: 'inspect_assets returned multimodal evidence.',
          tool_results: [
            {
              tool_call_id: 'tool-mm-1',
              name: 'inspect_assets',
              output: {
                summary: 'Agent extracted evidence from image, audio, and PDF assets.',
                content: [
                  {
                    type: 'image',
                    source_url: 'https://example.com/frame-17.png',
                    mime_type: 'image/png',
                    filename: 'frame-17.png',
                    caption: 'frame 17 shows the approval badge for release 417',
                    start_ms: 1200,
                    end_ms: 1800
                  },
                  {
                    type: 'audio',
                    source_url: 'https://example.com/meeting-snippet.mp3',
                    mime_type: 'audio/mpeg',
                    filename: 'meeting-snippet.mp3',
                    transcript: 'audio transcript says Alice approved the exception',
                    start_ms: 1180,
                    end_ms: 4820
                  },
                  {
                    type: 'file',
                    source_url: 'https://example.com/inspection-report.pdf',
                    mime_type: 'application/pdf',
                    filename: 'inspection-report.pdf',
                    parsed_text: 'inspection report confirms Chen owns rollback approval',
                    page_index: 2
                  }
                ]
              }
            }
          ]
        }
      ],
      parts: [
        {
          part_id: 'part:conv-mm-tool-1:001:000',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 1,
          part_index: 0,
          role: 'assistant',
          part_type: 'tool_use',
          text_content: 'inspect_assets',
          raw_payload: '{"prompt":"extract release evidence"}',
          metadata: {
            tool_call_id: 'tool-mm-1',
            name: 'inspect_assets'
          }
        },
        {
          part_id: 'part:conv-mm-tool-1:001:001',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 1,
          part_index: 1,
          role: 'assistant',
          part_type: 'image',
          mime_type: 'image/png',
          source_url: 'https://example.com/tool-diagram.png',
          filename: 'diagram-tool.png',
          text_content: 'diagram notes that Chen owns the rollback plan',
          metadata: {
            tool_call_id: 'tool-mm-1',
            tool_name: 'inspect_assets',
            tool_payload_kind: 'tool_input',
            tool_payload_path: 'tool_input.attachments[0]'
          }
        },
        {
          part_id: 'part:conv-mm-tool-1:001:002',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 1,
          part_index: 2,
          role: 'assistant',
          part_type: 'file',
          mime_type: 'application/pdf',
          source_url: 'https://example.com/tool-brief.pdf',
          filename: 'tool-brief.pdf',
          text_content: 'page 2 mentions Chen as the rollback owner',
          page_index: 2,
          metadata: {
            tool_call_id: 'tool-mm-1',
            tool_name: 'inspect_assets',
            tool_payload_kind: 'tool_input',
            tool_payload_path: 'tool_input.attachments[1]'
          }
        },
        {
          part_id: 'part:conv-mm-tool-1:002:000',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 2,
          part_index: 0,
          role: 'tool',
          part_type: 'tool_result',
          text_content: 'Agent extracted evidence from image, audio, and PDF assets.',
          raw_payload: '{"summary":"Agent extracted evidence from image, audio, and PDF assets."}',
          metadata: {
            tool_call_id: 'tool-mm-1',
            name: 'inspect_assets'
          }
        },
        {
          part_id: 'part:conv-mm-tool-1:002:001',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 2,
          part_index: 1,
          role: 'tool',
          part_type: 'image',
          mime_type: 'image/png',
          source_url: 'https://example.com/frame-17.png',
          filename: 'frame-17.png',
          text_content: 'frame 17 shows the approval badge for release 417',
          start_ms: 1200,
          end_ms: 1800,
          metadata: {
            tool_call_id: 'tool-mm-1',
            tool_name: 'inspect_assets',
            tool_payload_kind: 'tool_output',
            tool_payload_path: 'tool_output.content[0]'
          }
        },
        {
          part_id: 'part:conv-mm-tool-1:002:002',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 2,
          part_index: 2,
          role: 'tool',
          part_type: 'audio',
          mime_type: 'audio/mpeg',
          source_url: 'https://example.com/meeting-snippet.mp3',
          filename: 'meeting-snippet.mp3',
          text_content: 'audio transcript says Alice approved the exception',
          start_ms: 1180,
          end_ms: 4820,
          metadata: {
            tool_call_id: 'tool-mm-1',
            tool_name: 'inspect_assets',
            tool_payload_kind: 'tool_output',
            tool_payload_path: 'tool_output.content[1]'
          }
        },
        {
          part_id: 'part:conv-mm-tool-1:002:003',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          sequence_num: 2,
          part_index: 3,
          role: 'tool',
          part_type: 'file',
          mime_type: 'application/pdf',
          source_url: 'https://example.com/inspection-report.pdf',
          filename: 'inspection-report.pdf',
          text_content: 'inspection report confirms Chen owns rollback approval',
          page_index: 2,
          metadata: {
            tool_call_id: 'tool-mm-1',
            tool_name: 'inspect_assets',
            tool_payload_kind: 'tool_output',
            tool_payload_path: 'tool_output.content[2]'
          }
        }
      ],
      artifacts: [
        {
          artifact_id: 'artifact-mm-tool-image-caption',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          part_id: 'part:conv-mm-tool-1:002:001',
          artifact_type: 'image_caption',
          status: 'completed',
          text_content: 'frame 17 shows the approval badge for release 417'
        },
        {
          artifact_id: 'artifact-mm-tool-audio-transcript',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          part_id: 'part:conv-mm-tool-1:002:002',
          artifact_type: 'transcript',
          status: 'completed',
          text_content: 'audio transcript says Alice approved the exception',
          start_ms: 1180,
          end_ms: 4820
        },
        {
          artifact_id: 'artifact-mm-tool-file-parse',
          user_id: 'admin',
          conversation_id: 'conv-mm-tool-1',
          part_id: 'part:conv-mm-tool-1:002:003',
          artifact_type: 'document_text',
          status: 'completed',
          text_content: 'inspection report confirms Chen owns rollback approval',
          page_index: 2
        }
      ],
      metadata: {
        multimodal: true,
        source_retain: true
      },
      updatedAt: multimodalConversationAt,
      provider: 'openai_responses',
      requestFormat: 'responses',
      usage: { input_tokens: 32, output_tokens: 24, total_tokens: 56 }
    })
  };

  const memorySummary = {
    id: 'summary-1',
    node_id: 'node-summary-1',
    user_id: 'admin',
    conversation_id: 'conv-seed-1',
    type: 'summary',
    content: 'MemStation launch tracks gateway rollout, memory verification, and admin observability.',
    importance: 1.2,
    decay_factor: 0.95,
    metadata: {
      topic_label: 'Launch checklist',
      keywords: ['gateway', 'memory', 'observability']
    },
    related: [
      { node_id: 'node-fact-1', document_id: 'fact-1', document_type: 'fact', relation_type: 'supports', weight: 0.86 }
    ],
    created_at: createdAt,
    updated_at: createdAt
  };

  const fact = {
    id: 'fact-1',
    node_id: 'node-fact-1',
    user_id: 'admin',
    conversation_id: 'conv-seed-1',
    type: 'fact',
    content: 'Gateway smoke coverage is required before rollout.',
    importance: 1,
    decay_factor: 0.92,
    metadata: { confidence: 0.84 },
    related: [
      { node_id: 'node-summary-1', document_id: 'summary-1', document_type: 'summary', relation_type: 'supports', weight: 0.86 }
    ],
    created_at: createdAt,
    updated_at: createdAt
  };

  const impression = {
    id: 'impression-1',
    node_id: 'node-impression-1',
    user_id: 'admin',
    conversation_id: 'conv-seed-1',
    type: 'impression',
    content: 'The operator prefers explicit rollout checklists and traceable validation.',
    importance: 0.9,
    decay_factor: 0.9,
    metadata: {},
    related: [],
    created_at: createdAt,
    updated_at: createdAt
  };

  return {
    database: clone(databaseDefault),
    runtime_defaults: clone(runtimeDefaults),
    runtime: clone(runtimeDefaults),
    runtime_live: clone(runtimeDefaults),
    runtime_meta: clone(runtimeMeta),
    runtime_override_keys: {},
    provider_sets: Object.fromEntries(
      Object.entries(providerSetDefaults).map(([scope, providers]) => [
        scope,
        providers.map((provider) => ({
          ...provider,
          updated_at: now
        }))
      ])
    ),
    model_settings: clone(modelDefaults),
    users: [
      {
        user_id: 'admin',
        name: 'Admin User',
        email: 'admin@fixture.local',
        is_admin: true,
        created_at: createdAt
      }
    ],
    user_settings: {
      admin: {
        chat_model: 'gpt-4.1-mini',
        summary_model: 'gpt-4.1-mini',
        analysis_model: 'claude-3-7-sonnet',
        embedding_model: 'text-embedding-3-large',
        tool_write_enabled: false,
        max_injection_tokens: 2048
      }
    },
    api_keys: {
      admin: [
        {
          id: 'api-key-seed-1',
          user_id: 'admin',
          name: 'Playground key',
          prefix: 'ms-e2e',
          created_at: createdAt
        }
      ]
    },
    conversations,
    entity_aliases: [],
    memory_documents: {
      entity: [{id:'entity-station',node_id:'node-entity-station',user_id:'admin',generation_id:'generation-admin-active',type:'entity',content:'MemStation project',metadata:{title:'MemStation',entity_scope:'work'},created_at:now,updated_at:now}],
      summary: [memorySummary],
      fact: [fact],
      impression: [impression]
    },
    key_memories: [
      {
        id: 'key-1',
        user_id: 'admin',
        key: 'rollout_owner',
        value: 'ops-team',
        category: 'operations',
        source: 'analysis',
        review_status: 'approved',
        updated_at: now,
        metadata: { conversation_id: 'conv-seed-1' }
      }
    ],
    conversation_memories: {
      'conv-seed-1': {
        conversation_id: 'conv-seed-1',
        documents: [memorySummary, fact],
        key_memories: []
      }
    },
    previews: {},
    audits: [
      {
        id: 'audit-seed-1',
        actor_type: 'system',
        actor_id: 'fixture',
        action: 'memory_document_create',
        target_type: 'summary',
        target_id: 'summary-1',
        payload: { content: memorySummary.content },
        created_at: createdAt
      }
    ],
    queues: [
      { name: 'default', size: 2, pending: 1, active: 0, scheduled: 1, retry: 0, archived: 0, latency_ms: 42 },
      { name: 'memory', size: 3, pending: 1, active: 1, scheduled: 0, retry: 1, archived: 0, latency_ms: 88 }
    ],
    tasks: [
      {
        id: 'task-1',
        queue: 'default',
        type: 'conversation.finalize',
        state: 'scheduled',
        payload: { conversation_id: 'conv-seed-2', user_id: 'admin' },
        user_id: 'admin',
        conversation_id: 'conv-seed-2',
        retried: 0,
        max_retry: 10,
        next_process_at: isoOffset(-5)
      },
      {
        id: 'task-2',
        queue: 'memory',
        type: 'analysis.extract',
        state: 'retry',
        payload: { conversation_id: 'conv-seed-1', user_id: 'admin' },
        user_id: 'admin',
        conversation_id: 'conv-seed-1',
        last_error: 'fixture analysis timeout',
        retried: 2,
        max_retry: 10,
        last_failed_at: isoOffset(10),
        next_process_at: isoOffset(-2)
      },
      {
        id: 'task-3',
        queue: 'memory',
        type: 'summary.generate',
        state: 'active',
        payload: { conversation_id: 'conv-seed-1', user_id: 'admin' },
        user_id: 'admin',
        conversation_id: 'conv-seed-1',
        retried: 0,
        max_retry: 10,
        next_process_at: isoOffset(-1)
      }
    ],
    next_request_id: 1,
    next_key_memory_id: 2,
    next_api_key_id: 2,
    next_task_id: 4
  };
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url || '/', `http://${req.headers.host}`);

  if (req.method === 'OPTIONS') {
    setCORSHeaders(req, res);
    res.writeHead(204, {
      'Access-Control-Allow-Methods': 'GET,POST,PUT,DELETE,OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization, X-MemStation-Mode, X-MemStation-Session, X-MemStation-Inject-Context, X-MemStation-Inject-Tools, X-MemStation-Tool-Write, X-MemStation-Memory-Persist, X-MemStation-End-Conversation, X-MemStation-Provider',
      'Access-Control-Allow-Credentials': 'true'
    });
    res.end();
    return;
  }

  if (req.method === 'GET' && url.pathname === '/__health') {
    json(req, res, { status: 'ok' });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/__test/reset') {
    state = createState();
    json(req, res, { ok: true });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/admin/login') {
    const body = await readJSON(req);
    if (body.token !== adminToken && !verifyAdminTokenHash(adminToken, String(body.token || ''))) {
      json(req, res, { error: { message: 'invalid admin token' } }, 401);
      return;
    }
    json(req, res, { authenticated: true }, 200, {
      'Set-Cookie': `${sessionCookieName}=${sessionCookieValue}; Path=/; HttpOnly; SameSite=Lax`
    });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/admin/logout') {
    json(req, res, { authenticated: false }, 200, {
      'Set-Cookie': `${sessionCookieName}=; Path=/; HttpOnly; Max-Age=0; SameSite=Lax`
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/session') {
    json(req, res, { authenticated: isAdmin(req) });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/health/live') {
    json(req, res, { status: 'live', version: 'fixture', mode: 'gateway' });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/health/ready') {
    json(req, res, {
      status: 'ready',
      version: 'fixture',
      mode: 'gateway',
      services: {
        postgres: 'ok',
        redis: 'ok',
        vector: 'ok'
      }
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/v1/models') {
    if (!ensureGatewayAuth(req, res)) {
      return;
    }
    json(req, res, {
      object: 'list',
      data: [
        { id: state.model_settings.chat_model, object: 'model', provider: 'openai_compatible' },
        { id: state.model_settings.analysis_model, object: 'model', provider: 'anthropic' },
        { id: state.model_settings.embedding_model, object: 'model', provider: 'openai_compatible' }
      ]
    });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/v1/chat/completions') {
    if (!ensureGatewayAuth(req, res)) {
      return;
    }
    await handleGatewayRequest('chat_completions', req, res, await readJSON(req));
    return;
  }

  if (req.method === 'POST' && url.pathname === '/v1/responses') {
    if (!ensureGatewayAuth(req, res)) {
      return;
    }
    await handleGatewayRequest('responses', req, res, await readJSON(req));
    return;
  }

  if (req.method === 'POST' && url.pathname === '/v1/messages') {
    if (!ensureGatewayAuth(req, res)) {
      return;
    }
    await handleGatewayRequest('anthropic_messages', req, res, await readJSON(req));
    return;
  }

  if (req.method === 'POST' && url.pathname === '/graphql') {
    if (!ensureAdmin(req, res)) {
      return;
    }
    const body = await readJSON(req);
    const query = String(body.query || '');
    if (query.includes('searchMemories')) {
      const variables = body.variables || {};
      const result = searchMemories(String(variables.query || ''), Number(variables.topN || 10), [], 0, 'admin');
      json(req, res, {
        data: {
          searchMemories: {
            total: result.total,
            queryEmbeddingTokens: result.queryEmbeddingTokens,
            memories: result.memories.map((item) => ({
              id: item.id,
              type: item.type,
              content: item.content,
              score: item.score,
              effectiveScore: item.effectiveScore
            }))
          }
        }
      });
      return;
    }
    json(req, res, { errors: [{ message: 'unsupported graphql operation in fixture' }] }, 400);
    return;
  }

  if (!url.pathname.startsWith('/api/admin/')) {
    json(req, res, { error: { message: 'not found' } }, 404);
    return;
  }

  if (!ensureAdmin(req, res)) {
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/overview') {
    json(req, res, {
      active_conversations: Object.values(state.conversations).filter((item) => item.finalize_status === 'active' || item.finalize_status === 'ending').length,
      conversation_count: Object.keys(state.conversations).length,
      memory_count: Object.values(state.memory_documents).reduce((sum, items) => sum + items.length, 0),
      key_memory_count: state.key_memories.length,
      recent_audit_events: state.audits.length,
      config: { default_provider: state.runtime.default_provider },
      models: [
        { provider: 'openai_compatible', model: state.model_settings.chat_model },
        { provider: 'anthropic', model: state.model_settings.analysis_model }
      ]
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/stats/tokens') {
    json(req, res, { items: tokenStats() });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/users') {
    json(req, res, { items: userSummaries() });
    return;
  }

  const userDetailMatch = url.pathname.match(/^\/api\/admin\/users\/([^/]+)$/);
  if (req.method === 'GET' && userDetailMatch) {
    const userId = decodeURIComponent(userDetailMatch[1]);
    const user = state.users.find((item) => item.user_id === userId);
    if (!user) {
      json(req, res, { error: { message: 'user not found' } }, 404);
      return;
    }
    json(req, res, {
      user: {
        id: user.user_id,
        name: user.name,
        email: user.email,
        is_admin: user.is_admin,
        created_at: user.created_at
      },
      settings: clone(state.user_settings[userId] || {}),
      api_keys: clone(state.api_keys[userId] || []),
      conversations: conversationList(userId),
      key_memories: clone(state.key_memories.filter((item) => item.user_id === userId)),
      token_usage: tokenStats().find((item) => item.user_id === userId) || { input_tokens: 0, output_tokens: 0, total_tokens: 0 },
      stats: {
        api_key_count: (state.api_keys[userId] || []).length,
        conversation_count: conversationList(userId).length,
        key_memory_count: state.key_memories.filter((item) => item.user_id === userId).length
      }
    });
    return;
  }

  const userSourcesMatch = url.pathname.match(/^\/api\/admin\/users\/([^/]+)\/sources$/);
  if (req.method === 'GET' && userSourcesMatch) {
    const userId = decodeURIComponent(userSourcesMatch[1]);
    const user = state.users.find((item) => item.user_id === userId);
    if (!user) {
      json(req, res, { error: { message: 'user not found' } }, 404);
      return;
    }
    json(req, res, buildUserSources(userId));
    return;
  }

  const userKeysMatch = url.pathname.match(/^\/api\/admin\/users\/([^/]+)\/api-keys$/);
  if (req.method === 'POST' && userKeysMatch) {
    const userId = decodeURIComponent(userKeysMatch[1]);
    const body = await readJSON(req);
    const { record, plaintext } = nextAPIKey(userId, String(body.name || 'manager-created'));
    state.api_keys[userId] = [record, ...(state.api_keys[userId] || [])];
    addAudit('api_key_create', 'api_key', record.id, { user_id: userId, name: record.name, prefix: record.prefix });
    json(req, res, { record, api_key: plaintext });
    return;
  }

  const revokeKeyMatch = url.pathname.match(/^\/api\/admin\/users\/([^/]+)\/api-keys\/([^/]+)$/);
  if (req.method === 'DELETE' && revokeKeyMatch) {
    const userId = decodeURIComponent(revokeKeyMatch[1]);
    const keyId = decodeURIComponent(revokeKeyMatch[2]);
    const keys = state.api_keys[userId] || [];
    const record = keys.find((item) => item.id === keyId);
    if (!record) {
      json(req, res, { error: { message: 'api key not found' } }, 404);
      return;
    }
    record.revoked_at = nowISO();
    addAudit('api_key_revoke', 'api_key', keyId, { user_id: userId });
    noContent(req, res);
    return;
  }

  const userSettingsMatch = url.pathname.match(/^\/api\/admin\/users\/([^/]+)\/settings$/);
  if (req.method === 'PUT' && userSettingsMatch) {
    const userId = decodeURIComponent(userSettingsMatch[1]);
    const body = await readJSON(req);
    state.user_settings[userId] = {
      ...(state.user_settings[userId] || {}),
      ...body
    };
    addAudit('user_settings_update', 'user', userId, { settings: body });
    json(req, res, {
      settings: clone(state.user_settings[userId]),
      memory_rebuild_required: String(state.user_settings[userId]?.embedding_model || '') !== String(state.model_settings.embedding_model || '')
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/conversations') {
    const userId = url.searchParams.get('user_id');
    const limit = Number(url.searchParams.get('limit') || '100');
    json(req, res, { items: conversationList(userId).slice(0, limit) });
    return;
  }

  const conversationMatch = url.pathname.match(/^\/api\/admin\/conversations\/([^/]+)$/);
  if (req.method === 'GET' && conversationMatch) {
    const conversationId = decodeURIComponent(conversationMatch[1]);
    const item = state.conversations[conversationId];
    if (!item) {
      json(req, res, { error: { message: 'conversation not found' } }, 404);
      return;
    }
    json(req, res, item);
    return;
  }

  const conversationMemoryMatch = url.pathname.match(/^\/api\/admin\/conversations\/([^/]+)\/memory$/);
  if (req.method === 'GET' && conversationMemoryMatch) {
    const conversationId = decodeURIComponent(conversationMemoryMatch[1]);
    json(req, res, buildConversationMemory(conversationId));
    return;
  }

  const conversationSourcesMatch = url.pathname.match(/^\/api\/admin\/conversations\/([^/]+)\/sources$/);
  if (req.method === 'GET' && conversationSourcesMatch) {
    const conversationId = decodeURIComponent(conversationSourcesMatch[1]);
    const conversation = state.conversations[conversationId];
    if (!conversation) {
      json(req, res, { error: { message: 'conversation not found' } }, 404);
      return;
    }
    json(req, res, buildConversationSources(conversationId, conversation.user_id));
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/admin/playground/memory-preview') {
    const body = await readJSON(req);
    const conversationId = String(body.conversation_id || `preview-${randomUUID()}`);
    const messages = Array.isArray(body.messages) ? body.messages : [];
    const userText = messages.length > 0 ? String(messages[messages.length - 1]?.text || 'fixture preview') : 'fixture preview';
    const preview = state.previews[conversationId] || createPreview(conversationId, userText);
    state.previews[conversationId] = preview;
    json(req, res, preview);
    return;
  }

  if (url.pathname === '/api/admin/memory/entity-aliases') {
    const userId = url.searchParams.get('user_id') || 'admin';
    if (req.method === 'GET') { json(req, res, state.entity_aliases.filter((item) => item.user_id === userId)); return; }
    if (req.method === 'PUT') {
      const body = await readJSON(req);
      const existing = state.entity_aliases.find((item) => item.user_id === userId && item.alias === body.alias && item.target_node_id === body.target_node_id);
      const item = { ...body, user_id:userId, id:existing?.id || `alias-${state.entity_aliases.length + 1}` };
      if (existing) Object.assign(existing, item); else state.entity_aliases.push(item);
      addAudit('memory.entity_alias.update', 'entity_alias', item.id, {status:item.status});
      json(req, res, item); return;
    }
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/memory/documents') {
    const kind = url.searchParams.get('kind') || 'summary';
    const userId = url.searchParams.get('user_id');
    const limit = Number(url.searchParams.get('limit') || '200');
    const items = (state.memory_documents[kind] || []).filter((item) => !userId || item.user_id === userId).slice(0, limit);
    json(req, res, { items });
    return;
  }

  const memoryDocMatch = url.pathname.match(/^\/api\/admin\/memory\/documents\/(summary|fact|impression|entity)\/([^/]+)$/);
  if (memoryDocMatch) {
    const kind = memoryDocMatch[1];
    const id = decodeURIComponent(memoryDocMatch[2]);
    const item = findDocument(kind, id);
    if (!item) {
      json(req, res, { error: { message: 'memory document not found' } }, 404);
      return;
    }
    if (req.method === 'GET') {
      json(req, res, item);
      return;
    }
    if (req.method === 'PUT') {
      const body = await readJSON(req);
      item.content = String(body.content || item.content);
      item.updated_at = nowISO();
      addAudit('memory_document_update', kind, item.id, { content: item.content });
      json(req, res, item);
      return;
    }
  }

  if (req.method === 'POST' && url.pathname === '/api/admin/memory/search') {
    const body = await readJSON(req);
    const userId = url.searchParams.get('user_id') || undefined;
    const result = searchMemories(
      String(body.query || ''),
      Number(body.top_n || 10),
      Array.isArray(body.memory_types) ? body.memory_types.map(String) : [],
      Number(body.min_score || 0),
      userId
    );
    json(req, res, {
      total: result.total,
      queryEmbeddingTokens: result.queryEmbeddingTokens,
      query_embedding_tokens: result.queryEmbeddingTokens,
      memories: result.memories.map((item) => ({
        id: item.id,
        node_id: item.node_id,
        type: item.type,
        content: item.content,
        score: item.score,
        effectiveScore: item.effectiveScore,
        effective_score: item.effectiveScore,
        conversation_id: item.conversation_id,
        metadata: {
          ...(item.metadata || {}),
          matched_keywords: tokensFromQuery(String(body.query || '')).filter((token) =>
            Array.isArray(item.metadata?.keywords) && item.metadata.keywords.some((keyword) => String(keyword).toLowerCase().includes(token))
          )
        },
        related: item.related
      }))
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/memory/keys') {
    const userId = url.searchParams.get('user_id');
    const limit = Number(url.searchParams.get('limit') || '200');
    const items = state.key_memories.filter((item) => !userId || item.user_id === userId).slice(0, limit);
    json(req, res, { items });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/admin/memory/keys') {
    const body = await readJSON(req);
    const userId = url.searchParams.get('user_id') || 'admin';
    const item = {
      id: `key-${state.next_key_memory_id++}`,
      user_id: userId,
      key: String(body.key || ''),
      value: String(body.value || ''),
      category: String(body.category || ''),
      source: 'admin',
      review_status: String(body.review_status || 'pending'),
      updated_at: nowISO(),
      metadata: {}
    };
    state.key_memories.unshift(item);
    addAudit('key_memory_create', 'key_memory', item.id, { key: item.key, value: item.value, user_id: userId });
    json(req, res, item, 201);
    return;
  }

  const keyMemoryMatch = url.pathname.match(/^\/api\/admin\/memory\/keys\/([^/]+)$/);
  if (keyMemoryMatch) {
    const id = decodeURIComponent(keyMemoryMatch[1]);
    const userId = url.searchParams.get('user_id') || '';
    const item = findKeyMemory(id, userId);
    if (!item) {
      json(req, res, { error: { message: 'key memory not found' } }, 404);
      return;
    }
    if (req.method === 'GET') {
      json(req, res, item);
      return;
    }
    if (req.method === 'PUT') {
      const body = await readJSON(req);
      if (body.value !== undefined) {
        item.value = String(body.value);
      }
      if (body.category !== undefined) {
        item.category = String(body.category);
      }
      if (body.review_status !== undefined) {
        item.review_status = String(body.review_status);
      }
      item.updated_at = nowISO();
      addAudit('key_memory_update', 'key_memory', item.id, { value: item.value, category: item.category, review_status: item.review_status });
      json(req, res, item);
      return;
    }
    if (req.method === 'DELETE') {
      state.key_memories = state.key_memories.filter((entry) => entry.id !== id);
      addAudit('key_memory_delete', 'key_memory', id, { user_id: item.user_id, key: item.key });
      noContent(req, res);
      return;
    }
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/audit') {
    json(req, res, { items: filteredAudit(url.searchParams) });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/queues') {
    json(req, res, { queues: clone(state.queues) });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/config') {
    json(req, res, buildConfigPayload());
    return;
  }

  if (req.method === 'PUT' && url.pathname === '/api/admin/config') {
    const body = await readJSON(req);
    const runtimePatch = body.runtime || {};
    const nextRuntime = { ...state.runtime };
    const nextRuntimeOverrideKeys = { ...state.runtime_override_keys };
    for (const [key, value] of Object.entries(runtimePatch)) {
      if (value === null) {
        nextRuntime[key] = clone(state.runtime_defaults[key]);
        delete nextRuntimeOverrideKeys[key];
      } else if (key === 'cors_allowed_origins' && Array.isArray(value)) {
        nextRuntime[key] = value.map(String);
        nextRuntimeOverrideKeys[key] = true;
      } else if (value !== undefined) {
        nextRuntime[key] = value;
        nextRuntimeOverrideKeys[key] = true;
      }
    }
    state.runtime = nextRuntime;
    state.runtime_override_keys = nextRuntimeOverrideKeys;
    for (const [key, meta] of Object.entries(state.runtime_meta)) {
      if (meta.apply_mode === 'live' && key in runtimePatch) {
        state.runtime_live[key] = clone(state.runtime[key]);
      }
    }

    if (body.provider_sets && typeof body.provider_sets === 'object') {
      for (const [scope, providers] of Object.entries(body.provider_sets)) {
        const scopeProviders = providers && typeof providers === 'object' && providers.providers && typeof providers.providers === 'object'
          ? providers.providers
          : providers;
        if (!scopeProviders || typeof scopeProviders !== 'object') {
          continue;
        }
        for (const [providerName, providerPatch] of Object.entries(scopeProviders)) {
          applyProviderOverride(scope, providerName, providerPatch);
        }
      }
    }

    if (body.providers && typeof body.providers === 'object') {
      for (const [providerName, providerPatch] of Object.entries(body.providers)) {
        applyProviderOverride('gateway', providerName, providerPatch);
      }
    }

    if (body.database && typeof body.database === 'object') {
      if (body.database.clear) {
        state.database = clone(databaseDefault);
      } else if (typeof body.database.url === 'string') {
        state.database = databaseSnapshot(body.database.url, 'override');
      }
    }

    for (const key of Object.keys(state.model_settings)) {
      if (key in body) {
        if (body[key] === null) {
          state.model_settings[key] = clone(modelDefaults[key]);
        } else {
          state.model_settings[key] = body[key];
        }
      }
    }

    addAudit('system_config_update', 'system_config', 'runtime', { body });
    json(req, res, buildConfigPayload());
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/providers/diagnostics') {
    json(req, res, {
      items: buildProviderDiagnostics(),
      runtime: {
        desired_gateway_default_provider: state.runtime.default_provider,
        live_gateway_default_provider: state.runtime_live.default_provider,
        desired_internal_default_provider: state.runtime.internal_default_provider,
        live_internal_default_provider: state.runtime_live.internal_default_provider,
        desired_timeout_seconds: state.runtime.provider_timeout_seconds,
        live_timeout_seconds: state.runtime_live.provider_timeout_seconds
      }
    });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/tasks') {
    const limit = Number(url.searchParams.get('limit') || '20');
    const queue = url.searchParams.get('queue') || '';
    const states = (url.searchParams.get('states') || '').split(',').map((item) => item.trim()).filter(Boolean);
    json(req, res, { items: listTasks({ queue, states, limit }) });
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/tasks/detail') {
    const queue = url.searchParams.get('queue') || '';
    const id = url.searchParams.get('id') || '';
    const item = findTask(queue, id);
    if (!item) {
      json(req, res, { error: { message: 'task not found' } }, 404);
      return;
    }
    json(req, res, { item, queue_info: findQueue(queue) || undefined });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/admin/tasks/requeue') {
    const body = await readJSON(req);
    if (body.queue && body.task_id) {
      const task = findTask(String(body.queue), String(body.task_id));
      if (!task) {
        json(req, res, { error: { message: 'task not found' } }, 404);
        return;
      }
      task.state = 'pending';
      task.last_error = undefined;
      task.next_process_at = nowISO();
      addAudit('requeue_task', 'queue_task', task.id, { queue: task.queue, task_id: task.id });
      json(req, res, { ok: true, item: task });
      return;
    }

    const queue = body.task_type === 'conversation.finalize' ? 'default' : 'memory';
    const task = {
      id: `task-${state.next_task_id++}`,
      queue,
      type: String(body.task_type || 'analysis.extract'),
      state: 'pending',
      payload: clone(body),
      user_id: body.user_id ? String(body.user_id) : undefined,
      conversation_id: body.conversation_id ? String(body.conversation_id) : undefined,
      retried: 0,
      max_retry: 10,
      next_process_at: nowISO()
    };
    state.tasks.unshift(task);
    const queueInfo = findQueue(queue);
    if (queueInfo) {
      queueInfo.pending += 1;
      queueInfo.size += 1;
    }
    addAudit('requeue_task', 'queue_task', task.id, { queue, task_type: task.type, conversation_id: task.conversation_id, user_id: task.user_id });
    json(req, res, { ok: true, item: task }, 202);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/admin/cache') {
    json(req, res, {
      l1: { enabled: true, scope: 'process', ttl_seconds: state.runtime.cache_ttl_seconds },
      l2: { enabled: true, backend: 'redis', ttl_seconds: state.runtime.cache_ttl_seconds },
      pubsub: { enabled: true, channel: 'memstation.cache.invalidate', delivery: 'best-effort' },
      notes: [
        'L1 is process-local and invalidated by TTL or Redis fan-out.',
        'L2 is the shared Redis cache used for hot memory reads and config snapshots.'
      ],
      health: {
        status: 'ok',
        components: {
          redis: { status: 'ok' },
          l1: { status: 'ok' },
          pubsub: { status: 'ok' }
        }
      }
    });
    return;
  }

  json(req, res, { error: { message: 'not found' } }, 404);
});

server.listen(port, host, () => {
  console.log(`fixture target listening on http://${host}:${port}`);
});
