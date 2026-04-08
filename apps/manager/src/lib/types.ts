export type TargetKind = 'single' | 'cluster-entry' | 'node';
export type GatewayProtocol = 'chat_completions' | 'responses' | 'anthropic_messages';
export type GatewayMode = 'transparent' | 'inject';
export type ChatRole = 'system' | 'user' | 'assistant' | 'tool';

export interface MemStationTarget {
  id: string;
  label: string;
  baseUrl: string;
  environment: string;
  clusterName: string;
  region: string;
  kind: TargetKind;
}

export interface AdminSessionState {
  authenticated: boolean;
  checking: boolean;
  error?: string;
  checkedAt?: string;
}

export interface UsageRecord {
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
}

export interface ToolCall {
  id: string;
  name: string;
  arguments?: Record<string, unknown>;
}

export interface ToolResult {
  tool_call_id: string;
  name?: string;
  output?: Record<string, unknown>;
}

export interface ChatThreadMessage {
  id: string;
  role: ChatRole;
  text: string;
  blocks?: Array<{ type: string; text?: string; raw?: string }>;
  toolCalls?: ToolCall[];
  toolResults?: ToolResult[];
  thinking?: string;
  usage?: UsageRecord;
  pending?: boolean;
  error?: string;
  metadata?: Record<string, unknown>;
}

export interface GatewayHeaders {
  conversationId: string;
  requestId: string;
  memoryPersist: boolean;
  sourceRetain?: boolean;
}

export interface GatewayDebugState {
  requestHeaders: Record<string, string>;
  requestBody: Record<string, unknown>;
  rawEvents: Array<Record<string, unknown>>;
  rawResponse?: Record<string, unknown>;
}

export interface ConversationRecord {
  conversation_id: string;
  user_id: string;
  request_format: string;
  provider: string;
  mode: string;
  finalize_status: string;
  summary_status: string;
  analysis_status: string;
  messages: ChatThreadMessage[];
  segments?: ConversationSegment[];
  usage: UsageRecord;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ConversationSegment {
  id: string;
  generation_id?: string;
  user_id: string;
  conversation_id: string;
  segment_index: number;
  start_sequence: number;
  end_sequence: number;
  message_count: number;
  overlap_from_sequence: number;
  overlap_message_count: number;
  content: string;
  content_hash: string;
  metadata?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface ConversationListItem {
  conversation_id: string;
  user_id: string;
  provider: string;
  protocol: string;
  mode: string;
  finalize_status: string;
  summary_status: string;
  analysis_status: string;
  message_count: number;
  total_tokens: number;
  updated_at: string;
  metadata?: Record<string, unknown>;
}

export interface MemoryDocument {
  id: string;
  node_id: string;
  generation_id?: string;
  user_id: string;
  conversation_id?: string;
  type: string;
  content: string;
  score?: number;
  effective_score?: number;
  importance?: number;
  decay_factor?: number;
  activity_at?: string;
  time_tier?: string;
  metadata?: Record<string, unknown>;
  related?: Array<{ id: string; type: string; weight: number }>;
  created_at?: string;
  updated_at?: string;
}

export interface MemorySummaryRecord {
  id: string;
  summary: string;
  topicLabel?: string | null;
}

export interface ImpressionRecord {
  id: string;
  description: string;
}

export interface FactRecord {
  id: string;
  content: string;
}

export interface KeyMemoryRecord {
  id: string;
  user_id?: string;
  key: string;
  value: string;
  category?: string;
  source?: string;
  review_status?: string;
  importance?: number;
  decay_factor?: number;
  activity_at?: string;
  time_tier?: string;
  effective_score?: number;
  last_confirmed_at?: string;
  updated_at?: string;
  created_at?: string;
  metadata?: Record<string, unknown>;
}

export interface SearchMemoryItem {
  id: string;
  type: string;
  content: string;
  score: number;
  effectiveScore: number;
  node_id?: string;
  conversation_id?: string;
  importance?: number;
  decay_factor?: number;
  activity_at?: string;
  time_tier?: string;
  metadata?: Record<string, unknown>;
  related?: Array<{ id: string; type: string; weight: number }>;
}

export interface SearchMemoriesResult {
  total: number;
  queryEmbeddingTokens: number;
  memories: SearchMemoryItem[];
}

export interface ConversationMemoryPayload {
  conversation_id: string;
  generation_id?: string;
  documents: MemoryDocument[];
  key_memories: KeyMemoryRecord[];
}

export interface MemorySourceSnapshot {
  snapshot_id: string;
  user_id: string;
  conversation_id: string;
  snapshot_version: number;
  source_hash: string;
  request_format?: string;
  provider?: string;
  mode?: string;
  finalize_status?: string;
  message_count?: number;
  metadata?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface MemoryGeneration {
  generation_id: string;
  user_id: string;
  status: string;
  scope: string;
  conversation_id?: string;
  is_active: boolean;
  structure_version: number;
  embedding_version: number;
  embedding_model: string;
  activate_on_success: boolean;
  source_count: number;
  processed_count: number;
  embedding_count: number;
  failure_count: number;
  last_error?: string;
  metadata?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
  activated_at?: string;
}

export interface MemoryGenerationSource {
  id: string;
  generation_id: string;
  user_id: string;
  snapshot_id: string;
  conversation_id: string;
  source_hash: string;
  status: string;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
  processed_at?: string;
}

export interface UserSourcesPayload {
  user_id: string;
  source_stats: {
    snapshot_count: number;
    latest_snapshot_at?: string;
    latest_snapshot_user_id?: string;
  };
  active_generation: MemoryGeneration;
  generations: MemoryGeneration[];
}

export interface ConversationSourcesPayload {
  user_id: string;
  conversation_id: string;
  has_snapshot: boolean;
  latest_snapshot?: MemorySourceSnapshot;
  snapshots: MemorySourceSnapshot[];
}

export interface MemoryRebuildDetailPayload {
  item: MemoryGeneration;
  sources: MemoryGenerationSource[];
}

export interface MemoryPreviewPayload {
  conversation_id?: string;
  summary_status: string;
  analysis_status: string;
  keywords?: string[];
  summary?: Record<string, unknown>;
  fact?: Record<string, unknown>;
  impression?: Record<string, unknown>;
  key_memories?: Array<Record<string, unknown>>;
  metadata?: Record<string, unknown>;
}

export interface QueueInfo {
  name: string;
  size: number;
  pending: number;
  active: number;
  scheduled: number;
  retry: number;
  archived?: number;
  paused?: boolean;
  latency_ms?: number;
}

export interface AuditEventRecord {
  id: string;
  actor_type?: string;
  actor_id?: string;
  action: string;
  target_type: string;
  target_id: string;
  payload?: Record<string, unknown>;
  created_at?: string;
}

export interface DashboardOverview {
  active_conversations: number;
  conversation_count: number;
  memory_count?: number;
  key_memory_count: number;
  recent_audit_events: number;
  config?: Record<string, unknown>;
  models?: Array<Record<string, unknown>>;
}

export interface TokenStatsResponse {
  items: Array<{
    user_id: string;
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
  }>;
}

export interface HealthPayload {
  status: string;
  version?: string;
  mode?: string;
  services?: Record<string, string>;
}

export interface ProviderSummary {
  provider: string;
  base_url: string;
  has_api_key: boolean;
  source: string;
  updated_at?: string;
}

export interface ProviderDiagnostic {
  provider: string;
  base_url: string;
  host?: string;
  scheme?: string;
  has_api_key: boolean;
  source: string;
  updated_at?: string;
  is_desired_default: boolean;
  is_live_default: boolean;
  desired_timeout_seconds: number;
  live_timeout_seconds: number;
  restart_required: boolean;
  status: 'ready' | 'warning' | 'degraded';
  issues: string[];
}

export interface RuntimeFieldMeta {
  apply_mode: 'live' | 'restart';
  source: 'default' | 'override';
  category: string;
  description: string;
}

export interface SystemRuntimeConfig {
  default_provider: string;
  vector_store_backend: string;
  conversation_timeout_seconds: number;
  provider_timeout_seconds: number;
  message_threshold: number;
  token_threshold: number;
  max_context_tokens: number;
  max_injection_tokens: number;
  rate_limit_rps: number;
  rate_limit_burst: number;
  cache_ttl_seconds: number;
  cors_allowed_origins: string[];
  session_cookie_cross_site: boolean;
}

export interface APIKeyRecord {
  id: string;
  user_id: string;
  name: string;
  prefix: string;
  revoked_at?: string;
  created_at: string;
}

export interface UserSummary {
  user_id: string;
  name: string;
  is_admin: boolean;
  api_key_count: number;
  conversation_count: number;
  key_memory_count: number;
  total_tokens: number;
  recent_active_at?: string;
  created_at: string;
}

export interface UserDetailPayload {
  user: {
    id: string;
    name: string;
    email?: string;
    is_admin: boolean;
    created_at: string;
  };
  settings: Record<string, unknown>;
  api_keys: APIKeyRecord[];
  conversations: ConversationListItem[];
  key_memories: KeyMemoryRecord[];
  token_usage: {
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
  };
  stats: {
    api_key_count: number;
    conversation_count: number;
    key_memory_count: number;
  };
}

export interface UpdateUserSettingsResponse {
  settings: Record<string, unknown>;
  memory_rebuild_required?: boolean;
}

export interface AdminConfigPayload extends Record<string, unknown> {
  providers?: ProviderSummary[];
  runtime_defaults?: SystemRuntimeConfig;
  runtime?: SystemRuntimeConfig;
  runtime_live?: SystemRuntimeConfig;
  runtime_meta?: Record<string, RuntimeFieldMeta>;
}

export interface QueueTaskRecord {
  id: string;
  queue: string;
  type: string;
  state: string;
  payload?: Record<string, unknown>;
  user_id?: string;
  conversation_id?: string;
  last_error?: string;
  retried?: number;
  max_retry?: number;
  last_failed_at?: string;
  next_process_at?: string;
  is_orphaned?: boolean;
}

export interface QueueTaskDetailPayload {
  item: QueueTaskRecord;
  queue_info?: QueueInfo;
}

export interface CacheStatusPayload {
  l1: {
    enabled: boolean;
    scope: string;
    ttl_seconds: number;
  };
  l2: {
    enabled: boolean;
    backend: string;
    ttl_seconds: number;
  };
  pubsub: {
    enabled: boolean;
    channel: string;
    delivery: string;
  };
  notes: string[];
  health?: {
    status: string;
    components?: Record<string, { status: string; error?: string }>;
  };
}
