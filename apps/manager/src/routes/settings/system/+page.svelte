<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, fetchHealth, saveConfig } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AdminConfigPayload, HealthPayload, RuntimeFieldMeta, SystemRuntimeConfig } from '$lib/types';

  type RuntimeKey = keyof SystemRuntimeConfig;

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
    'cache_ttl_seconds'
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
  let error = '';
  let success = '';
  let lastLoadKey = '';
  let ready: HealthPayload | null = null;
  let runtimeDefaults = emptyRuntime();
  let runtime = emptyRuntime();
  let runtimeLive = emptyRuntime();
  let runtimeMeta: Record<string, RuntimeFieldMeta> = {};
  let corsAllowedOriginsText = '';
  let allowedMimeTypesText = '';
  let pendingRestartCount = 0;
  let liveAppliedCount = 0;

  onMount(async () => {
    await loadData();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadData();
  }

  $: pendingRestartCount = allRuntimeKeys().filter((key) => runtimeDiffers(key) && metaFor(key).apply_mode === 'restart').length;
  $: liveAppliedCount = allRuntimeKeys().filter((key) => runtimeDiffers(key) && metaFor(key).apply_mode === 'live').length;

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

  function applyConfig(payload: AdminConfigPayload): void {
    runtimeDefaults = normalizeRuntime(payload.runtime_defaults ?? payload.runtime);
    runtime = normalizeRuntime(payload.runtime);
    runtimeLive = normalizeRuntime(payload.runtime_live ?? payload.runtime);
    runtimeMeta = payload.runtime_meta ?? {};
    corsAllowedOriginsText = runtime.cors_allowed_origins.join('\n');
    allowedMimeTypesText = runtime.multimodal_allowed_mime_types.join('\n');
  }

  function metaFor(key: RuntimeKey): RuntimeFieldMeta {
    return runtimeMeta[key] ?? {
      apply_mode: 'restart',
      source: 'default',
      category: 'runtime',
      description: ''
    };
  }

  function runtimeDiffers(key: RuntimeKey): boolean {
    if (Array.isArray(runtime[key])) {
      return (runtime[key] as string[]).join(',') !== (runtimeLive[key] as string[]).join(',');
    }
    return String(runtime[key]) !== String(runtimeLive[key]);
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
      const nextPendingRestartCount = allRuntimeKeys().filter((key) => runtimeDiffers(key) && metaFor(key).apply_mode === 'restart').length;
      success = nextPendingRestartCount > 0
        ? 'System configuration saved. Some changes are staged and require a target restart before they become effective.'
        : 'System configuration saved and live-update fields were applied immediately.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save system settings';
    } finally {
      saving = false;
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
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings / System</span>
    <h1 class="page-title">Control deployment-level runtime policy for the selected MemStation target</h1>
    <p class="page-description">
      Write runtime overrides directly to the target, review which changes apply immediately, and separate staged restart-required settings from live operational controls.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect or modify system settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading system settings...</div>
  {:else}
    <div class="kpi-grid">
      <div class="kpi-card">
        <span class="kpi-label">Runtime fields</span>
        <span class="kpi-value">{allRuntimeKeys().length}</span>
        <span class="kpi-meta">Deployment-level controls tracked by the target.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Live applied</span>
        <span class="kpi-value">{liveAppliedCount}</span>
        <span class="kpi-meta">Fields currently diverging from the original process defaults but already active.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Restart staged</span>
        <span class="kpi-value">{pendingRestartCount}</span>
        <span class="kpi-meta">Saved fields whose live process value still differs from the desired override.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Readiness</span>
        <span class="kpi-value">{ready?.status ?? 'unknown'}</span>
        <span class="kpi-meta">Current target dependency posture.</span>
      </div>
    </div>

    {#if success}
      <div class="notice-card">{success}</div>
    {/if}

    <div class="inline-group" style="justify-content: flex-end;">
      <button class="button-ghost" type="button" disabled={saving} on:click={resetAll}>Reset all overrides</button>
    </div>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Traffic control</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Provider selection, finalize policy, and upstream latency guards</h2>
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
            <span class="field-label">Conversation timeout (seconds)</span>
            <input class="input" type="number" min="0" bind:value={runtime.conversation_timeout_seconds} />
          </label>
          <label class="field">
            <span class="field-label">Provider timeout (seconds)</span>
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

        <div class="runtime-field-list">
          {#each trafficFields as key}
            <div class="runtime-field-row">
              <div>
                <strong>{key}</strong>
                <div class="helper-text">{metaFor(key).description}</div>
                <div class="helper-text">Default value: {formatRuntimeValue(key, defaultValueFor(key))}</div>
                <div class="helper-text">Live value: {formatRuntimeValue(key, runtimeLive[key])}</div>
              </div>
              <div class="runtime-field-status">
                <StatusBadge tone={metaFor(key).apply_mode === 'live' ? 'success' : 'warning'} label={metaFor(key).apply_mode} />
                <StatusBadge tone={metaFor(key).source === 'override' ? 'brand' : 'muted'} label={metaFor(key).source} />
                {#if runtimeDiffers(key)}
                  <StatusBadge tone={metaFor(key).apply_mode === 'restart' ? 'warning' : 'brand'} label={metaFor(key).apply_mode === 'restart' ? 'restart pending' : 'applied'} />
                {/if}
                <button class="button-ghost" type="button" disabled={saving || metaFor(key).source !== 'override'} on:click={() => resetField(key)}>Reset</button>
                <button class="button-ghost" type="button" disabled={saving} on:click={() => setLocalDefault(key)}>Use default</button>
              </div>
            </div>
          {/each}
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Memory and web</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Context budgets, cache, and browser-facing controls</h2>
          </div>
          <StatusBadge tone={ready?.status === 'ready' ? 'success' : 'warning'} label={ready?.status ?? 'unknown'} />
        </div>

        <div class="form-grid two-up">
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
            <span class="field-label">Cache TTL (seconds)</span>
            <input class="input" type="number" min="0" bind:value={runtime.cache_ttl_seconds} />
          </label>
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
            <span class="field-help">One origin per line or separated by commas. Leave empty to clear the override.</span>
          </label>
        </div>

        <div class="runtime-field-list">
          {#each [...memoryFields, ...webFields] as key}
            <div class="runtime-field-row">
              <div>
                <strong>{key}</strong>
                <div class="helper-text">{metaFor(key).description}</div>
                <div class="helper-text">Default value: {formatRuntimeValue(key, defaultValueFor(key))}</div>
                <div class="helper-text">Live value: {formatRuntimeValue(key, runtimeLive[key])}</div>
              </div>
              <div class="runtime-field-status">
                <StatusBadge tone={metaFor(key).apply_mode === 'live' ? 'success' : 'warning'} label={metaFor(key).apply_mode} />
                <StatusBadge tone={metaFor(key).source === 'override' ? 'brand' : 'muted'} label={metaFor(key).source} />
                {#if runtimeDiffers(key)}
                  <StatusBadge tone={metaFor(key).apply_mode === 'restart' ? 'warning' : 'brand'} label={metaFor(key).apply_mode === 'restart' ? 'restart pending' : 'applied'} />
                {/if}
                <button class="button-ghost" type="button" disabled={saving || metaFor(key).source !== 'override'} on:click={() => resetField(key)}>Reset</button>
                <button class="button-ghost" type="button" disabled={saving} on:click={() => setLocalDefault(key)}>Use default</button>
              </div>
            </div>
          {/each}
        </div>
      </section>
    </div>

    <section class="card page-stack">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Multimodal pipeline</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Blob retention, extractor models, and ingestion guardrails</h2>
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
            <option value="s3">s3</option>
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
          <span class="field-help">One MIME type per line or separated by commas. This allow-list gates hosted uploads and extraction.</span>
        </label>
      </div>

      <div class="runtime-field-list">
        {#each multimodalFields as key}
          <div class="runtime-field-row">
            <div>
              <strong>{key}</strong>
              <div class="helper-text">{metaFor(key).description}</div>
              <div class="helper-text">Default value: {formatRuntimeValue(key, defaultValueFor(key))}</div>
              <div class="helper-text">Live value: {formatRuntimeValue(key, runtimeLive[key])}</div>
            </div>
            <div class="runtime-field-status">
              <StatusBadge tone={metaFor(key).apply_mode === 'live' ? 'success' : 'warning'} label={metaFor(key).apply_mode} />
              <StatusBadge tone={metaFor(key).source === 'override' ? 'brand' : 'muted'} label={metaFor(key).source} />
              {#if runtimeDiffers(key)}
                <StatusBadge tone={metaFor(key).apply_mode === 'restart' ? 'warning' : 'brand'} label={metaFor(key).apply_mode === 'restart' ? 'restart pending' : 'applied'} />
              {/if}
              <button class="button-ghost" type="button" disabled={saving || metaFor(key).source !== 'override'} on:click={() => resetField(key)}>Reset</button>
              <button class="button-ghost" type="button" disabled={saving} on:click={() => setLocalDefault(key)}>Use default</button>
            </div>
          </div>
        {/each}
      </div>
    </section>

    <section class="card page-stack">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Dependency posture</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Readiness details for the selected target</h2>
        </div>
        <div class="runtime-field-status">
          {#if pendingRestartCount > 0}
            <StatusBadge tone="warning" label={`${pendingRestartCount} restart pending`} />
          {/if}
          {#if liveAppliedCount > 0}
            <StatusBadge tone="brand" label={`${liveAppliedCount} live override${liveAppliedCount > 1 ? 's' : ''}`} />
          {/if}
        </div>
      </div>
      <div class="grid-3">
        {#each Object.entries(ready?.services ?? {}) as [name, status]}
          <div class="debug-block">
            <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
              <strong>{name}</strong>
              <StatusBadge tone={status === 'ok' ? 'success' : 'warning'} label={status} />
            </div>
            <div class="helper-text">Runtime settings write through the target itself; this panel does not proxy or cache admin mutations.</div>
          </div>
        {/each}
      </div>
      <div class="inline-group" style="justify-content: flex-end;">
        <button class="button" type="button" disabled={saving} on:click={persist}>{saving ? 'Saving...' : 'Save system settings'}</button>
      </div>
    </section>
  {/if}
</section>
