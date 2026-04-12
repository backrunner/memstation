<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, fetchHealth, saveConfig } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type {
    AdminConfigPayload,
    DatabaseConfigSnapshot,
    HealthPayload,
    RuntimeFieldMeta,
    SystemRuntimeConfig
  } from '$lib/types';

  type RuntimeKey = keyof SystemRuntimeConfig;
  type StatusTone = 'brand' | 'success' | 'warning' | 'danger' | 'muted';

  const trafficFields: RuntimeKey[] = [
    'default_provider',
    'internal_default_provider',
    'conversation_timeout_seconds',
    'provider_timeout_seconds',
    'message_threshold',
    'token_threshold'
  ];
  const memoryFields: RuntimeKey[] = [
    'vector_store_backend',
    'max_context_tokens',
    'max_injection_tokens',
    'memory_relation_max_hops',
    'memory_relation_hop_decay_lambda',
    'memory_relation_hop_decay_exponent',
    'cache_ttl_seconds'
  ];
  const rerankFields: RuntimeKey[] = [
    'rerank_model',
    'rerank_candidate_multiplier',
    'rerank_min_candidates',
    'rerank_max_candidates',
    'rerank_blend_weight'
  ];
  const webFields: RuntimeKey[] = [
    'rate_limit_rps',
    'rate_limit_burst',
    'session_cookie_cross_site',
    'cors_allowed_origins'
  ];
  const multimodalFields: RuntimeKey[] = [
    'multimodal_enabled',
    'multimodal_storage_backend',
    'multimodal_storage_local_root',
    'multimodal_storage_bucket',
    'multimodal_extract_image_model',
    'multimodal_extract_audio_model',
    'multimodal_extract_video_model',
    'multimodal_extract_file_model',
    'multimodal_audio_transcription_enabled',
    'multimodal_video_frame_sampling_enabled',
    'multimodal_max_asset_bytes',
    'multimodal_allowed_mime_types'
  ];

  let loading = true;
  let saving = false;
  let databaseSaving = false;
  let error = '';
  let success = '';
  let lastLoadKey = '';
  let ready: HealthPayload | null = null;
  let database = emptyDatabase();
  let databaseInput = '';
  let runtimeDefaults = emptyRuntime();
  let runtimeSaved = emptyRuntime();
  let runtime = emptyRuntime();
  let runtimeLive = emptyRuntime();
  let runtimeMeta: Record<string, RuntimeFieldMeta> = {};
  let corsAllowedOriginsText = '';
  let allowedMimeTypesText = '';
  let liveAppliedCount = 0;
  let dirtyCount = 0;
  let overrideCount = 0;

  onMount(async () => {
    await loadData();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadData();
  }

  $: {
    runtimeSaved;
    runtimeLive;
    runtimeDefaults;
    runtimeMeta;
    runtime;
    liveAppliedCount = allRuntimeKeys().filter((key) => syncStateFor(key) === 'live_active').length;
    dirtyCount = allRuntimeKeys().filter((key) => draftDiffers(key)).length;
    overrideCount = allRuntimeKeys().filter((key) => isCustomized(key)).length;
  }

  function emptyRuntime(): SystemRuntimeConfig {
    return {
      default_provider: 'openai_compatible',
      internal_default_provider: 'openai_compatible',
      vector_store_backend: 'pgvector',
      conversation_timeout_seconds: 900,
      provider_timeout_seconds: 60,
      message_threshold: 32,
      token_threshold: 64000,
      max_context_tokens: 8192,
      max_injection_tokens: 2048,
      rerank_model: '',
      rerank_candidate_multiplier: 6,
      rerank_min_candidates: 20,
      rerank_max_candidates: 50,
      rerank_blend_weight: 0.35,
      memory_relation_max_hops: 2,
      memory_relation_hop_decay_lambda: 0.35,
      memory_relation_hop_decay_exponent: 1.25,
      rate_limit_rps: 20,
      rate_limit_burst: 40,
      cache_ttl_seconds: 120,
      cors_allowed_origins: [],
      session_cookie_cross_site: false,
      multimodal_enabled: true,
      multimodal_storage_backend: 'local',
      multimodal_storage_local_root: './data/blobs',
      multimodal_storage_bucket: '',
      multimodal_extract_image_model: '',
      multimodal_extract_audio_model: '',
      multimodal_extract_video_model: '',
      multimodal_extract_file_model: '',
      multimodal_audio_transcription_enabled: true,
      multimodal_video_frame_sampling_enabled: true,
      multimodal_max_asset_bytes: 104857600,
      multimodal_allowed_mime_types: []
    };
  }

  function emptyDatabase(): DatabaseConfigSnapshot {
    return {
      configured: false,
      source: 'unset',
      status: 'inactive'
    };
  }

  function allRuntimeKeys(): RuntimeKey[] {
    return [
      'default_provider',
      'internal_default_provider',
      'vector_store_backend',
      'conversation_timeout_seconds',
      'provider_timeout_seconds',
      'message_threshold',
      'token_threshold',
      'max_context_tokens',
      'max_injection_tokens',
      'rerank_model',
      'rerank_candidate_multiplier',
      'rerank_min_candidates',
      'rerank_max_candidates',
      'rerank_blend_weight',
      'memory_relation_max_hops',
      'memory_relation_hop_decay_lambda',
      'memory_relation_hop_decay_exponent',
      'rate_limit_rps',
      'rate_limit_burst',
      'cache_ttl_seconds',
      'cors_allowed_origins',
      'session_cookie_cross_site',
      'multimodal_enabled',
      'multimodal_storage_backend',
      'multimodal_storage_local_root',
      'multimodal_storage_bucket',
      'multimodal_extract_image_model',
      'multimodal_extract_audio_model',
      'multimodal_extract_video_model',
      'multimodal_extract_file_model',
      'multimodal_audio_transcription_enabled',
      'multimodal_video_frame_sampling_enabled',
      'multimodal_max_asset_bytes',
      'multimodal_allowed_mime_types'
    ];
  }

  function normalizeRuntime(value: unknown): SystemRuntimeConfig {
    const runtimeValue = (value ?? {}) as Partial<SystemRuntimeConfig>;
    return {
      default_provider: String(runtimeValue.default_provider ?? 'openai_compatible'),
      internal_default_provider: String(runtimeValue.internal_default_provider ?? 'openai_compatible'),
      vector_store_backend: String(runtimeValue.vector_store_backend ?? 'pgvector'),
      conversation_timeout_seconds: Number(runtimeValue.conversation_timeout_seconds ?? 900),
      provider_timeout_seconds: Number(runtimeValue.provider_timeout_seconds ?? 60),
      message_threshold: Number(runtimeValue.message_threshold ?? 32),
      token_threshold: Number(runtimeValue.token_threshold ?? 64000),
      max_context_tokens: Number(runtimeValue.max_context_tokens ?? 8192),
      max_injection_tokens: Number(runtimeValue.max_injection_tokens ?? 2048),
      rerank_model: String(runtimeValue.rerank_model ?? ''),
      rerank_candidate_multiplier: Number(runtimeValue.rerank_candidate_multiplier ?? 6),
      rerank_min_candidates: Number(runtimeValue.rerank_min_candidates ?? 20),
      rerank_max_candidates: Number(runtimeValue.rerank_max_candidates ?? 50),
      rerank_blend_weight: Number(runtimeValue.rerank_blend_weight ?? 0.35),
      memory_relation_max_hops: Number(runtimeValue.memory_relation_max_hops ?? 2),
      memory_relation_hop_decay_lambda: Number(runtimeValue.memory_relation_hop_decay_lambda ?? 0.35),
      memory_relation_hop_decay_exponent: Number(runtimeValue.memory_relation_hop_decay_exponent ?? 1.25),
      rate_limit_rps: Number(runtimeValue.rate_limit_rps ?? 20),
      rate_limit_burst: Number(runtimeValue.rate_limit_burst ?? 40),
      cache_ttl_seconds: Number(runtimeValue.cache_ttl_seconds ?? 120),
      cors_allowed_origins: Array.isArray(runtimeValue.cors_allowed_origins)
        ? runtimeValue.cors_allowed_origins.map((item) => String(item)).filter(Boolean)
        : [],
      session_cookie_cross_site: Boolean(runtimeValue.session_cookie_cross_site),
      multimodal_enabled: Boolean(runtimeValue.multimodal_enabled ?? true),
      multimodal_storage_backend: String(runtimeValue.multimodal_storage_backend ?? 'local'),
      multimodal_storage_local_root: String(runtimeValue.multimodal_storage_local_root ?? './data/blobs'),
      multimodal_storage_bucket: String(runtimeValue.multimodal_storage_bucket ?? ''),
      multimodal_extract_image_model: String(runtimeValue.multimodal_extract_image_model ?? ''),
      multimodal_extract_audio_model: String(runtimeValue.multimodal_extract_audio_model ?? ''),
      multimodal_extract_video_model: String(runtimeValue.multimodal_extract_video_model ?? ''),
      multimodal_extract_file_model: String(runtimeValue.multimodal_extract_file_model ?? ''),
      multimodal_audio_transcription_enabled: Boolean(runtimeValue.multimodal_audio_transcription_enabled ?? true),
      multimodal_video_frame_sampling_enabled: Boolean(runtimeValue.multimodal_video_frame_sampling_enabled ?? true),
      multimodal_max_asset_bytes: Number(runtimeValue.multimodal_max_asset_bytes ?? 104857600),
      multimodal_allowed_mime_types: Array.isArray(runtimeValue.multimodal_allowed_mime_types)
        ? runtimeValue.multimodal_allowed_mime_types.map((item) => String(item)).filter(Boolean)
        : []
    };
  }

  function normalizeDatabase(value: unknown): DatabaseConfigSnapshot {
    const databaseValue = (value ?? {}) as Partial<DatabaseConfigSnapshot>;
    return {
      configured: Boolean(databaseValue.configured),
      source: String(databaseValue.source ?? 'unset'),
      status: String(databaseValue.status ?? 'inactive'),
      display: databaseValue.display ? String(databaseValue.display) : undefined,
      last_error: databaseValue.last_error ? String(databaseValue.last_error) : undefined,
      updated_at: databaseValue.updated_at ? String(databaseValue.updated_at) : undefined
    };
  }

  function applyConfig(payload: AdminConfigPayload): void {
    database = normalizeDatabase(payload.database);
    databaseInput = '';
    runtimeDefaults = normalizeRuntime(payload.runtime_defaults ?? payload.runtime);
    runtimeSaved = normalizeRuntime(payload.runtime);
    runtime = normalizeRuntime(payload.runtime);
    runtimeLive = normalizeRuntime(payload.runtime_live ?? payload.runtime);
    runtimeMeta = payload.runtime_meta ?? {};
    corsAllowedOriginsText = runtime.cors_allowed_origins.join('\n');
    allowedMimeTypesText = runtime.multimodal_allowed_mime_types.join('\n');
  }

  function metaFor(key: RuntimeKey): RuntimeFieldMeta {
    return runtimeMeta[key] ?? {
      apply_mode: 'live',
      source: 'default',
      category: 'runtime',
      description: ''
    };
  }

  function valuesEqual(left: SystemRuntimeConfig[RuntimeKey], right: SystemRuntimeConfig[RuntimeKey]): boolean {
    if (Array.isArray(left) && Array.isArray(right)) {
      return left.join(',') === right.join(',');
    }
    return String(left) === String(right);
  }

  function runtimeDiffers(key: RuntimeKey): boolean {
    return !valuesEqual(runtimeSaved[key], runtimeLive[key]);
  }

  function draftDiffers(key: RuntimeKey): boolean {
    return !valuesEqual(runtime[key], runtimeSaved[key]);
  }

  function isCustomized(key: RuntimeKey): boolean {
    return !valuesEqual(runtimeSaved[key], runtimeDefaults[key]);
  }

  function syncStateFor(key: RuntimeKey): 'restart_pending' | 'restart_active' | 'live_active' | 'live_drift' | 'default' {
    const meta = metaFor(key);
    const differs = runtimeDiffers(key);
    if (!isCustomized(key)) {
      return differs && meta.apply_mode === 'live' ? 'live_drift' : 'default';
    }
    if (meta.apply_mode === 'restart') {
      return differs ? 'restart_pending' : 'restart_active';
    }
    return differs ? 'live_drift' : 'live_active';
  }

  function formatRuntimeValue(key: RuntimeKey, value: SystemRuntimeConfig[RuntimeKey]): string {
    if (Array.isArray(value)) {
      return value.length > 0 ? value.join(', ') : 'none';
    }
    if (typeof value === 'boolean') {
      return value ? 'enabled' : 'disabled';
    }
    return String(value);
  }

  function defaultValueFor(key: RuntimeKey): SystemRuntimeConfig[RuntimeKey] {
    return runtimeDefaults[key];
  }

  function revertDraft(key: RuntimeKey): void {
    if (key === 'cors_allowed_origins' || key === 'multimodal_allowed_mime_types') {
      if (key === 'cors_allowed_origins') {
        runtime = { ...runtime, cors_allowed_origins: [...runtimeSaved.cors_allowed_origins] };
        corsAllowedOriginsText = runtimeSaved.cors_allowed_origins.join('\n');
      } else {
        runtime = { ...runtime, multimodal_allowed_mime_types: [...runtimeSaved.multimodal_allowed_mime_types] };
        allowedMimeTypesText = runtimeSaved.multimodal_allowed_mime_types.join('\n');
      }
      return;
    }
    runtime = { ...runtime, [key]: runtimeSaved[key] } as SystemRuntimeConfig;
  }

  function setLocalDefault(key: RuntimeKey): void {
    if (key === 'cors_allowed_origins' || key === 'multimodal_allowed_mime_types') {
      if (key === 'cors_allowed_origins') {
        runtime = { ...runtime, cors_allowed_origins: [...runtimeDefaults.cors_allowed_origins] };
        corsAllowedOriginsText = runtimeDefaults.cors_allowed_origins.join('\n');
      } else {
        runtime = { ...runtime, multimodal_allowed_mime_types: [...runtimeDefaults.multimodal_allowed_mime_types] };
        allowedMimeTypesText = runtimeDefaults.multimodal_allowed_mime_types.join('\n');
      }
      return;
    }
    runtime = { ...runtime, [key]: runtimeDefaults[key] } as SystemRuntimeConfig;
  }

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      const [configPayload, readyPayload] = await Promise.all([
        fetchConfig($selectedTarget),
        fetchHealth($selectedTarget, '/health/ready')
      ]);
      applyConfig(configPayload);
      ready = readyPayload;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load system settings';
    } finally {
      loading = false;
    }
  }

  async function persist(): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const payload = await saveConfig($selectedTarget, {
        runtime: {
          ...runtime,
          cors_allowed_origins: corsAllowedOriginsText
            .split(/\n|,/)
            .map((item) => item.trim())
            .filter(Boolean),
          multimodal_allowed_mime_types: allowedMimeTypesText
            .split(/\n|,/)
            .map((item) => item.trim())
            .filter(Boolean)
        }
      });
      applyConfig(payload);
      success = 'System settings applied to the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save system settings';
    } finally {
      saving = false;
    }
  }

  async function persistDatabase(): Promise<void> {
    if (!$selectedTarget || !databaseInput.trim()) {
      return;
    }
    databaseSaving = true;
    error = '';
    success = '';
    try {
      const payload = await saveConfig($selectedTarget, {
        database: {
          url: databaseInput.trim()
        }
      });
      applyConfig(payload);
      success = 'Database connection updated and applied to the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to update database connection';
    } finally {
      databaseSaving = false;
    }
  }

  async function clearDatabase(): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    databaseSaving = true;
    error = '';
    success = '';
    try {
      const payload = await saveConfig($selectedTarget, {
        database: {
          clear: true
        }
      });
      applyConfig(payload);
      success = 'Database override cleared. The target is now using the deployment default, if one exists.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to clear database connection override';
    } finally {
      databaseSaving = false;
    }
  }

  async function resetField(key: RuntimeKey): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const payload = await saveConfig($selectedTarget, {
        runtime: {
          [key]: null
        }
      });
      applyConfig(payload);
      success = `${key} reverted to the deployment default.`;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : `Failed to reset ${key}`;
    } finally {
      saving = false;
    }
  }

  async function resetAll(): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const payload = await saveConfig($selectedTarget, {
        runtime: Object.fromEntries(allRuntimeKeys().map((key) => [key, null]))
      });
      applyConfig(payload);
      success = 'All system runtime overrides were cleared back to deployment defaults.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to reset runtime overrides';
    } finally {
      saving = false;
    }
  }

  function trackedRuntimeKeys(): RuntimeKey[] {
    return allRuntimeKeys().filter((key) => draftDiffers(key) || isCustomized(key) || runtimeDiffers(key));
  }

  function displayKeyName(key: RuntimeKey): string {
    const labels: Partial<Record<RuntimeKey, string>> = {
      default_provider: 'Gateway provider',
      internal_default_provider: 'Internal provider',
      vector_store_backend: 'Vector store',
      conversation_timeout_seconds: 'Conversation timeout',
      provider_timeout_seconds: 'Provider timeout',
      message_threshold: 'Message threshold',
      token_threshold: 'Token threshold',
      max_context_tokens: 'Context budget',
      max_injection_tokens: 'Injection budget',
      rerank_model: 'Rerank model',
      rerank_candidate_multiplier: 'Candidate multiplier',
      rerank_min_candidates: 'Min rerank candidates',
      rerank_max_candidates: 'Max rerank candidates',
      rerank_blend_weight: 'Blend weight',
      memory_relation_max_hops: 'Relation max hops',
      memory_relation_hop_decay_lambda: 'Hop decay lambda',
      memory_relation_hop_decay_exponent: 'Hop decay exponent',
      rate_limit_rps: 'Rate limit RPS',
      rate_limit_burst: 'Rate limit burst',
      cache_ttl_seconds: 'Cache TTL',
      cors_allowed_origins: 'CORS origins',
      session_cookie_cross_site: 'Cross-site cookie',
      multimodal_enabled: 'Multimodal ingestion',
      multimodal_storage_backend: 'Blob storage',
      multimodal_storage_local_root: 'Local blob root',
      multimodal_storage_bucket: 'Remote bucket',
      multimodal_extract_image_model: 'Image extractor',
      multimodal_extract_audio_model: 'Audio extractor',
      multimodal_extract_video_model: 'Video extractor',
      multimodal_extract_file_model: 'File extractor',
      multimodal_audio_transcription_enabled: 'Audio transcription',
      multimodal_video_frame_sampling_enabled: 'Video frame sampling',
      multimodal_max_asset_bytes: 'Max asset bytes',
      multimodal_allowed_mime_types: 'Allowed MIME types'
    };
    return labels[key] ?? key;
  }

  function summaryTextFor(key: RuntimeKey): string {
    const draftValue = formatRuntimeValue(key, runtime[key]);
    const savedValue = formatRuntimeValue(key, runtimeSaved[key]);
    const liveValue = formatRuntimeValue(key, runtimeLive[key]);
    const defaultValue = formatRuntimeValue(key, runtimeDefaults[key]);

    if (draftDiffers(key)) {
      return `Draft ${draftValue} · Saved ${savedValue}`;
    }
    if (runtimeDiffers(key)) {
      return `Saved ${savedValue} · Live ${liveValue}`;
    }
    if (isCustomized(key)) {
      return `Saved ${savedValue} · Default ${defaultValue}`;
    }
    return `Draft ${draftValue}`;
  }

  function databaseTone(status: string): StatusTone {
    switch (status) {
      case 'ready':
        return 'success';
      case 'degraded':
        return 'danger';
      case 'inactive':
        return 'muted';
      default:
        return 'warning';
    }
  }

  function databaseSourceLabel(source: string): string {
    switch (source) {
      case 'override':
        return 'Manager override';
      case 'env':
        return 'Environment default';
      case 'unset':
        return 'Not configured';
      default:
        return source;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings / System</span>
    <h1 class="page-title">System runtime</h1>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect or modify system settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading system settings...</div>
  {:else}
    <section class="card page-stack">
      <div class="page-header">
        <div>
          <h2 class="page-title" style="font-size: 1.35rem;">Database connection</h2>
        </div>
        <StatusBadge tone={databaseTone(database.status)} label={database.status} />
      </div>

      <div class="grid-2 system-layout-grid">
        <div class="page-stack" style="gap: 10px;">
          <div class="debug-block">
            <strong>{database.display ?? 'No database connection is configured.'}</strong>
            <div class="helper-text">{databaseSourceLabel(database.source)}</div>
            {#if database.updated_at}
              <div class="helper-text">Updated {new Date(database.updated_at).toLocaleString()}</div>
            {/if}
          </div>
          {#if database.last_error}
            <div class="error-state" style="margin: 0;">{database.last_error}</div>
          {/if}
        </div>

        <div class="page-stack" style="gap: 12px;">
          <label class="field">
            <span class="field-label">Connection URI</span>
            <input
              class="input"
              bind:value={databaseInput}
              placeholder="postgres://user:password@127.0.0.1:5432/memstation?sslmode=disable"
            />
          </label>
          <div class="inline-group system-actions" style="justify-content: flex-end;">
            <button
              class="button-ghost"
              type="button"
              disabled={databaseSaving || database.source !== 'override'}
              on:click={clearDatabase}
            >
              Clear override
            </button>
            <button class="button" type="button" disabled={databaseSaving || !databaseInput.trim()} on:click={persistDatabase}>
              {databaseSaving ? 'Applying...' : 'Apply database'}
            </button>
          </div>
        </div>
      </div>
    </section>

    <div class="summary-strip">
      <div class="summary-chip"><strong>{overrideCount}</strong> overrides</div>
      <div class="summary-chip"><strong>{dirtyCount}</strong> unsaved</div>
      <div class="summary-chip"><strong>{liveAppliedCount}</strong> live</div>
      <div class="summary-chip"><strong>{ready?.status ?? 'unknown'}</strong> readiness</div>
    </div>

    {#if success}
      <div class="notice-card">{success}</div>
    {/if}

    <div class="inline-group system-actions" style="justify-content: flex-end;">
      <button class="button-ghost" type="button" disabled={saving} on:click={resetAll}>Reset all overrides</button>
      <button class="button" type="button" disabled={saving} on:click={persist}>{saving ? 'Saving...' : 'Save system settings'}</button>
    </div>

    <div class="grid-2 system-layout-grid">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <h2 class="page-title" style="font-size: 1.35rem;">Traffic</h2>
          </div>
        </div>

        <div class="form-grid two-up">
          <label class="field">
            <span class="field-label">Gateway default provider</span>
            <select class="input" bind:value={runtime.default_provider}>
              <option value="openai_compatible">openai_compatible</option>
              <option value="openai_responses">openai_responses</option>
              <option value="anthropic">anthropic</option>
              <option value="gemini">gemini</option>
            </select>
          </label>
          <label class="field">
            <span class="field-label">Internal default provider</span>
            <select class="input" bind:value={runtime.internal_default_provider}>
              <option value="openai_compatible">openai_compatible</option>
              <option value="openai_responses">openai_responses</option>
              <option value="anthropic">anthropic</option>
              <option value="gemini">gemini</option>
            </select>
          </label>
          <label class="field">
            <span class="field-label">Conversation timeout</span>
            <input class="input" type="number" min="0" bind:value={runtime.conversation_timeout_seconds} />
          </label>
          <label class="field">
            <span class="field-label">Provider timeout</span>
            <input class="input" type="number" min="0" bind:value={runtime.provider_timeout_seconds} />
          </label>
          <label class="field">
            <span class="field-label">Message threshold</span>
            <input class="input" type="number" min="0" bind:value={runtime.message_threshold} />
          </label>
          <label class="field">
            <span class="field-label">Token threshold</span>
            <input class="input" type="number" min="0" bind:value={runtime.token_threshold} />
          </label>
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div>
            <h2 class="page-title" style="font-size: 1.35rem;">Memory and web</h2>
          </div>
        </div>

        <div class="form-grid two-up">
          <div class="settings-subsection">
            <div class="sidebar-label">Recall budgets</div>
          </div>
          <label class="field">
            <span class="field-label">Vector store backend</span>
            <select class="input" bind:value={runtime.vector_store_backend}>
              <option value="pgvector">pgvector</option>
            </select>
          </label>
          <label class="field">
            <span class="field-label">Max context tokens</span>
            <input class="input" type="number" min="0" bind:value={runtime.max_context_tokens} />
          </label>
          <label class="field">
            <span class="field-label">Max injection tokens</span>
            <input class="input" type="number" min="0" bind:value={runtime.max_injection_tokens} />
          </label>
          <label class="field">
            <span class="field-label">Cache TTL</span>
            <input class="input" type="number" min="0" bind:value={runtime.cache_ttl_seconds} />
          </label>
          <div class="settings-subsection">
            <div class="sidebar-label">Reranking</div>
          </div>
          <label class="field">
            <span class="field-label">Rerank model</span>
            <input class="input" bind:value={runtime.rerank_model} placeholder="cohere:rerank-v3.5" />
          </label>
          <label class="field">
            <span class="field-label">Candidate multiplier</span>
            <input class="input" type="number" min="1" max="16" bind:value={runtime.rerank_candidate_multiplier} />
          </label>
          <label class="field">
            <span class="field-label">Min rerank candidates</span>
            <input class="input" type="number" min="1" bind:value={runtime.rerank_min_candidates} />
          </label>
          <label class="field">
            <span class="field-label">Max rerank candidates</span>
            <input class="input" type="number" min="1" bind:value={runtime.rerank_max_candidates} />
          </label>
          <label class="field">
            <span class="field-label">Rerank blend weight</span>
            <input class="input" type="number" min="0" max="1" step="0.05" bind:value={runtime.rerank_blend_weight} />
          </label>
          <div class="settings-subsection">
            <div class="sidebar-label">Relation expansion</div>
          </div>
          <label class="field">
            <span class="field-label">Relation max hops</span>
            <input class="input" type="number" min="1" max="4" bind:value={runtime.memory_relation_max_hops} />
          </label>
          <label class="field">
            <span class="field-label">Hop decay lambda</span>
            <input class="input" type="number" min="0" max="2" step="0.05" bind:value={runtime.memory_relation_hop_decay_lambda} />
          </label>
          <label class="field">
            <span class="field-label">Hop decay exponent</span>
            <input class="input" type="number" min="0.5" max="3" step="0.05" bind:value={runtime.memory_relation_hop_decay_exponent} />
          </label>
          <div class="settings-subsection">
            <div class="sidebar-label">Web controls</div>
          </div>
          <label class="field">
            <span class="field-label">Rate limit RPS</span>
            <input class="input" type="number" min="0" bind:value={runtime.rate_limit_rps} />
          </label>
          <label class="field">
            <span class="field-label">Rate limit burst</span>
            <input class="input" type="number" min="0" bind:value={runtime.rate_limit_burst} />
          </label>
          <label class="field">
            <span class="field-label">Cross-site admin cookie</span>
            <div class="inline-group">
              <button class={runtime.session_cookie_cross_site ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.session_cookie_cross_site = true)}>Enabled</button>
              <button class={!runtime.session_cookie_cross_site ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.session_cookie_cross_site = false)}>Disabled</button>
            </div>
          </label>
          <label class="field" style="grid-column: 1 / -1;">
            <span class="field-label">CORS allowed origins</span>
            <textarea class="textarea" rows="5" bind:value={corsAllowedOriginsText} placeholder="https://manager.example.com\nhttp://localhost:5173"></textarea>
          </label>
        </div>
      </section>
    </div>

    <section class="card page-stack">
        <div class="page-header">
          <div>
            <h2 class="page-title" style="font-size: 1.35rem;">Multimodal</h2>
          </div>
        </div>

      <div class="form-grid two-up">
        <label class="field">
          <span class="field-label">Multimodal ingestion</span>
          <div class="inline-group">
            <button class={runtime.multimodal_enabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.multimodal_enabled = true)}>Enabled</button>
            <button class={!runtime.multimodal_enabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.multimodal_enabled = false)}>Disabled</button>
          </div>
        </label>
        <label class="field">
          <span class="field-label">Blob storage backend</span>
          <select class="input" bind:value={runtime.multimodal_storage_backend}>
            <option value="local">local</option>
          </select>
        </label>
        <label class="field">
          <span class="field-label">Local blob root</span>
          <input class="input" bind:value={runtime.multimodal_storage_local_root} />
        </label>
        <label class="field">
          <span class="field-label">Remote bucket</span>
          <input class="input" bind:value={runtime.multimodal_storage_bucket} placeholder="memstation-assets" />
        </label>
        <label class="field">
          <span class="field-label">Image extractor model</span>
          <input class="input" bind:value={runtime.multimodal_extract_image_model} placeholder="configured vision model" />
        </label>
        <label class="field">
          <span class="field-label">Audio extractor model</span>
          <input class="input" bind:value={runtime.multimodal_extract_audio_model} placeholder="configured ASR model" />
        </label>
        <label class="field">
          <span class="field-label">Video extractor model</span>
          <input class="input" bind:value={runtime.multimodal_extract_video_model} placeholder="configured video model" />
        </label>
        <label class="field">
          <span class="field-label">File extractor model</span>
          <input class="input" bind:value={runtime.multimodal_extract_file_model} placeholder="configured document model" />
        </label>
        <label class="field">
          <span class="field-label">Max asset bytes</span>
          <input class="input" type="number" min="0" bind:value={runtime.multimodal_max_asset_bytes} />
        </label>
        <label class="field">
          <span class="field-label">Audio transcription</span>
          <div class="inline-group">
            <button class={runtime.multimodal_audio_transcription_enabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.multimodal_audio_transcription_enabled = true)}>Enabled</button>
            <button class={!runtime.multimodal_audio_transcription_enabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.multimodal_audio_transcription_enabled = false)}>Disabled</button>
          </div>
        </label>
        <label class="field">
          <span class="field-label">Video frame sampling</span>
          <div class="inline-group">
            <button class={runtime.multimodal_video_frame_sampling_enabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.multimodal_video_frame_sampling_enabled = true)}>Enabled</button>
            <button class={!runtime.multimodal_video_frame_sampling_enabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (runtime.multimodal_video_frame_sampling_enabled = false)}>Disabled</button>
          </div>
        </label>
        <label class="field" style="grid-column: 1 / -1;">
          <span class="field-label">Allowed MIME types</span>
          <textarea class="textarea" rows="6" bind:value={allowedMimeTypesText} placeholder="image/png&#10;application/pdf&#10;audio/mpeg"></textarea>
        </label>
      </div>
    </section>

    <section class="card page-stack">
        <div class="page-header">
          <div>
            <h2 class="page-title" style="font-size: 1.35rem;">Changes</h2>
          </div>
        </div>

      {#if trackedRuntimeKeys().length === 0}
        <div class="empty-state">Everything is using deployment defaults.</div>
      {:else}
        <div class="runtime-field-list compact-runtime-list">
          {#each trackedRuntimeKeys() as key}
            <div class="runtime-field-row compact-runtime-row">
              <div>
                <strong>{displayKeyName(key)}</strong>
                <div class="helper-text">{summaryTextFor(key)}</div>
              </div>
              <div class="runtime-field-status">
                {#if draftDiffers(key)}
                  <button class="button-ghost" type="button" disabled={saving} on:click={() => revertDraft(key)}>Revert</button>
                {/if}
                {#if isCustomized(key)}
                  <button class="button-ghost" type="button" disabled={saving} on:click={() => resetField(key)}>Clear</button>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>

<style>
  .system-actions {
    flex-wrap: wrap;
  }

  .system-layout-grid {
    align-items: start;
  }

  .settings-subsection {
    grid-column: 1 / -1;
    margin-top: 8px;
    padding-top: 10px;
    border-top: 1px solid var(--border-soft);
  }

  .settings-subsection:first-child {
    margin-top: 0;
    padding-top: 0;
    border-top: 0;
  }

  .compact-runtime-list {
    gap: 10px;
  }

  .compact-runtime-row {
    align-items: center;
    padding-block: 12px;
  }
</style>
