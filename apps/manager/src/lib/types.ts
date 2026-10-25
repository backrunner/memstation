export type TargetKind = 'single' | 'cluster-entry' | 'node';
export type GatewayProtocol = 'chat_completions' | 'responses' | 'anthropic_messages';
export type GatewayMode = 'transparent' | 'inject';
export type ChatRole = 'system' | 'user' | 'assistant' | 'tool';
export type JSONValue = string | number | boolean | null | { [key: string]: JSONValue } | JSONValue[];

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
  arguments?: JSONValue;
}

export interface ToolResult {
  tool_call_id: string;
  name?: string;
  output?: JSONValue;
}

export interface ContentBlock {
  type: string;
  mime_type?: string;
  blob_id?: string;
  source_url?: string;
  provider_file_id?: string;
  filename?: string;
  size_bytes?: number;
  sha256?: string;
  page_index?: number;
  start_ms?: number;
  end_ms?: number;
  duration_ms?: number;
  text?: string;
  raw?: string;
  metadata?: Record<string, unknown>;
}

export interface ChatThreadMessage {
  id: string;
  role: ChatRole;
  text: string;
  blocks?: ContentBlock[];
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
  parts?: MessagePart[];
  artifacts?: MessagePartArtifact[];
  segments?: ConversationSegment[];
  usage: UsageRecord;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface MessagePart {
  part_id: string;
  user_id: string;
  conversation_id: string;
  sequence_num: number;
  part_index: number;
  role: ChatRole;
  part_type: string;
  mime_type?: string;
  blob_id?: string;
  source_url?: string;
  provider_file_id?: string;
  filename?: string;
  text_content?: string;
  raw_payload?: string;
  page_index?: number;
  start_ms?: number;
  end_ms?: number;
  duration_ms?: number;
  metadata?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface MessagePartArtifact {
  artifact_id: string;
  user_id: string;
  conversation_id: string;
  part_id: string;
  artifact_type: string;
  status: string;
  extractor_kind?: string;
  model_name?: string;
  text_content?: string;
  page_index?: number;
  start_ms?: number;
  end_ms?: number;
  confidence?: number;
  metadata?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface MemoryEvidence {
  id: string;
  generation_id?: string;
  user_id: string;
  node_id: string;
  conversation_id: string;
  sequence_num: number;
  part_id?: string;
  artifact_id?: string;
  page_index?: number;
  start_ms?: number;
  end_ms?: number;
  evidence_kind: string;
  excerpt_text?: string;
  weight?: number;
  metadata?: Record<string, unknown>;
  created_at?: string;
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

export type GenericMemoryDocumentKind = 'event' | 'task' | 'procedure' | 'artifact' | 'entity' | 'agent_action';
export type CoreMemoryDocumentKind = 'summary' | 'segment' | 'impression' | 'fact';
export type MemoryDocumentKind = CoreMemoryDocumentKind | GenericMemoryDocumentKind;
export type SearchMemoryType = MemoryDocumentKind | 'key_memory';

export interface RelatedMemory {
  weight: number;
  node_id?: string;
  document_id?: string;
  document_type?: string;
  relation_type?: string;
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
  lineage?: MemoryLineage;
  related?: RelatedMemory[];
  evidences?: MemoryEvidence[];
  created_at?: string;
  updated_at?: string;
}

export interface MemoryLineage {
  fact_key?: string;
  lineage_group_id?: string;
  revision_seq?: number;
  generation_id?: string;
  lineage_path?: string[];
  superseded_alias_node_ids?: string[];
  revision_depth?: number;
  current_head_id?: string;
}

export interface MemorySummaryRecord {
  id: string;
  user_id?: string;
  summary: string;
  topicLabel?: string | null;
}

export interface ImpressionRecord {
  id: string;
  description: string;
}

export interface FactRecord {
  id: string;
  user_id?: string;
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
  user_id?: string;
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
  lineage?: MemoryLineage;
  related?: RelatedMemory[];
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
  facts?: Array<Record<string, unknown>>;
  impression?: Record<string, unknown>;
  generic_items?: Array<Record<string, unknown>>;
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
  scope?: string;
  provider: string;
  base_url: string;
  has_api_key: boolean;
  source: string;
  updated_at?: string;
}

export interface ProviderSetPayload {
  gateway?: ProviderSummary[];
  internal?: ProviderSummary[];
  embedding?: ProviderSummary[];
  rerank?: ProviderSummary[];
}

export interface ProviderDiagnostic {
  scope: string;
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

export interface DatabaseConfigSnapshot {
  configured: boolean;
  source: 'unset' | 'env' | 'override' | string;
  status: 'inactive' | 'ready' | 'degraded' | string;
  display?: string;
  last_error?: string;
  updated_at?: string;
}

export interface SystemRuntimeConfig {
  default_provider: string;
  internal_default_provider: string;
  vector_store_backend: string;
  conversation_timeout_seconds: number;
  provider_timeout_seconds: number;
  message_threshold: number;
  token_threshold: number;
  max_context_tokens: number;
  max_injection_tokens: number;
  rerank_model: string;
  rerank_candidate_multiplier: number;
  rerank_min_candidates: number;
  rerank_max_candidates: number;
  rerank_blend_weight: number;
  memory_relation_max_hops: number;
  memory_relation_hop_decay_lambda: number;
  memory_relation_hop_decay_exponent: number;
  rate_limit_rps: number;
  rate_limit_burst: number;
  cache_ttl_seconds: number;
  cors_allowed_origins: string[];
  session_cookie_cross_site: boolean;
  multimodal_enabled: boolean;
  multimodal_storage_backend: string;
  multimodal_storage_local_root: string;
  multimodal_storage_bucket: string;
  multimodal_extract_image_model: string;
  multimodal_extract_audio_model: string;
  multimodal_extract_video_model: string;
  multimodal_extract_file_model: string;
  multimodal_audio_transcription_enabled: boolean;
  multimodal_video_frame_sampling_enabled: boolean;
  multimodal_max_asset_bytes: number;
  multimodal_allowed_mime_types: string[];
}

export interface APIKeyRecord {
  agent_id?: string;
  agent_name?: string;
  default_workspace_id?: string;
  capabilities?: string[];
  expires_at?: string;
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
  identity_mode?: "personal_only";
  agents?: { id: string; name: string; status: string; legacy: boolean }[];
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
  provider_sets?: ProviderSetPayload;
  database?: DatabaseConfigSnapshot;
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

export interface AgentRecord {
  id: string;
  user_id: string;
  actor_id: string;
  name: string;
  status: 'active' | 'disabled';
  default_workspace_id: string;
  revision: number;
  legacy: boolean;
  created_at: string;
  updated_at: string;
}

export interface AgentGrant {
  user_id: string;
  workspace_id: string;
  agent_id: string;
  capabilities: string[];
  revision: number;
}

export interface AgentDetail {
  agent: AgentRecord;
  grant: AgentGrant;
  workspace: { id: string; name: string; status: string; capabilities: string[]; policy_version: number };
  effective_capabilities: string[];
}

export interface AgentCredential extends APIKeyRecord { effective_capabilities: string[] }
export interface CursorPage<T> { items: T[]; next_cursor: string }

export interface WorkspaceRecord {
  id: string;
  user_id: string;
  name: string;
  kind: string;
  status: string;
  capabilities: string[];
  policy_version: number;
  created_at: string;
  updated_at: string;
}
export interface WorkspacePage extends CursorPage<WorkspaceRecord> { workspace_mode: 'personal_only' | 'workspace' }
