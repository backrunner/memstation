<svelte:options runes={false} />

<script lang="ts">
  import RerankModelField from '#lib/components/settings/RerankModelField.svelte';
  import Button from '#lib/components/ui/Button.svelte';
  import { onMount } from 'svelte';

  import { fetchConfig, fetchHealth, saveConfig } from '#lib/api/client.ts';
  import ConfirmActionDialog from '#lib/components/ui/ConfirmActionDialog.svelte';
  import SettingsSection from '#lib/components/layout/SettingsSection.svelte';
  import SettingsNavigation from '#lib/components/layout/SettingsNavigation.svelte';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import SwitchField from '#lib/components/ui/SwitchField.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import TextareaField from '#lib/components/ui/TextareaField.svelte';
  import { isMimeType, isNonNegativeInteger, isNumberInRange, isOriginValue, isPositiveInteger, isPostgresURL, parseLineList } from '#lib/forms/validation.ts';
  import {
    buildSessionDraftKey,
    clearSessionDraft,
    draftsEqual,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '#lib/stores/session-drafts.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type {
    AdminConfigPayload,
    DatabaseConfigSnapshot,
    HealthPayload,
    RuntimeFieldMeta,
    SystemRuntimeConfig
  } from '#lib/types.ts';

  type RuntimeKey = keyof SystemRuntimeConfig;
  type SystemSettingsDraft = {
    runtime: SystemRuntimeConfig;
    databaseInput: string;
    corsAllowedOriginsText: string;
    allowedMimeTypesText: string;
  };

  const llmProviderItems = [
    { value: 'openai_compatible', label: 'openai_compatible' },
    { value: 'openai_responses', label: 'openai_responses' },
    { value: 'anthropic', label: 'anthropic' },
    { value: 'gemini', label: 'gemini' }
  ];

  const sections = [
    { id: 'system-traffic', label: 'Traffic' }, { id: 'system-recall', label: 'Recall' },
    { id: 'system-rerank', label: 'Rerank' }, { id: 'system-relations', label: 'Relations' },
    { id: 'system-web', label: 'Web' }, { id: 'system-multimodal', label: 'Multimodal' },
    { id: 'system-database', label: 'Database connection' }, { id: 'system-overrides', label: 'Overrides & drift' }
  ];

  function observeSheetMask(sheet: HTMLElement) {
    const mask = sheet.querySelector<HTMLElement>('.settings-scroll-mask')!;
    let frame = 0;
    const update = () => {
      frame = 0;
      const boundary = Number.parseFloat(getComputedStyle(mask).top);
      sheet.dataset.scrolled = String(sheet.getBoundingClientRect().top + 1 < boundary);
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(update); };
    const observer = new ResizeObserver(schedule);
    observer.observe(sheet);
    const header = sheet.closest('.page-stack')?.querySelector(':scope > .page-header');
    if (header) observer.observe(header);
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule, { passive: true });
    schedule();
    return { destroy() {
      cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
    } };
  }
  const vectorStoreItems = [{ value: 'pgvector', label: 'pgvector' }];
  const storageBackendItems = [{ value: 'local', label: 'local' }];
  const systemDraftVersion = 1;

  let loading = true;
  let saving = false;
  let databaseSaving = false;
  let error = '';
  let success = '';
  let confirmOpen = false;
  let confirmTitle = '';
  let confirmDescription = '';
  let confirmSubject = '';
  let confirmLabel = '';
  let pendingConfirmedAction: (() => Promise<void>) | null = null;
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
  let fieldErrors: Record<string, string> = {};
  let activeDraftKey = '';
  let draftReady = false;
  let hydratingDraft = false;
  let savedDraft = emptySystemDraft();
  let currentDraft = emptySystemDraft();

  onMount(async () => {
    await loadData();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: systemDraftKey = buildSessionDraftKey('settings.system', $selectedTarget?.id);
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadData();
  }
  $: currentDraft = {
    runtime: normalizeRuntime(runtime),
    databaseInput,
    corsAllowedOriginsText,
    allowedMimeTypesText
  };
  $: if (draftReady && activeDraftKey && !hydratingDraft) {
    if (draftsEqual(currentDraft, savedDraft)) {
      clearSessionDraft(activeDraftKey);
    } else {
      writeSessionDraft(activeDraftKey, systemDraftVersion, currentDraft);
    }
  }

  useSessionDraftUnloadWarning(
    () => draftReady && !draftsEqual(currentDraft, savedDraft),
    'These system settings have unsaved changes. Closing or reloading the page will discard the draft kept in this tab.'
  );

  useSessionDraftPersistence(() => {
    if (!activeDraftKey) {
      return;
    }
    const snapshot = captureDraft();
    if (draftsEqual(snapshot, savedDraft)) {
      clearSessionDraft(activeDraftKey);
      return;
    }
    writeSessionDraft(activeDraftKey, systemDraftVersion, snapshot);
  });

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

  function emptySystemDraft(): SystemSettingsDraft {
    return {
      runtime: emptyRuntime(),
      databaseInput: '',
      corsAllowedOriginsText: '',
      allowedMimeTypesText: ''
    };
  }

  function captureDraft(): SystemSettingsDraft {
    return {
      runtime: normalizeRuntime(runtime),
      databaseInput,
      corsAllowedOriginsText,
      allowedMimeTypesText
    };
  }

  function applyDraft(draft: SystemSettingsDraft | null): void {
    if (!draft) {
      return;
    }
    runtime = normalizeRuntime(draft.runtime);
    databaseInput = draft.databaseInput ?? '';
    corsAllowedOriginsText = draft.corsAllowedOriginsText ?? runtime.cors_allowed_origins.join('\n');
    allowedMimeTypesText = draft.allowedMimeTypesText ?? runtime.multimodal_allowed_mime_types.join('\n');
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
    fieldErrors = {};
  }

  function runtimeFieldError(key: RuntimeKey): string {
    return fieldErrors[`runtime.${key}`] ?? '';
  }

  function databaseFieldError(key: string): string {
    return fieldErrors[`database.${key}`] ?? '';
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

  function countLiveApplied(
    current: SystemRuntimeConfig,
    saved: SystemRuntimeConfig,
    defaults: SystemRuntimeConfig,
    live: SystemRuntimeConfig,
    meta: Record<string, RuntimeFieldMeta>
  ): number {
    return allRuntimeKeys().filter((key) => {
      const fieldMeta = meta[key] ?? { apply_mode: 'live', source: 'default', category: 'runtime', description: '' };
      const customized = !valuesEqual(saved[key], defaults[key]);
      const differs = !valuesEqual(saved[key], live[key]);
      if (!customized) {
        return false;
      }
      if (fieldMeta.apply_mode === 'restart') {
        return !differs;
      }
      return !differs && valuesEqual(current[key], saved[key]);
    }).length;
  }

  function countDirty(current: SystemRuntimeConfig, saved: SystemRuntimeConfig): number {
    return allRuntimeKeys().filter((key) => !valuesEqual(current[key], saved[key])).length;
  }

  function countOverrides(saved: SystemRuntimeConfig, defaults: SystemRuntimeConfig): number {
    return allRuntimeKeys().filter((key) => !valuesEqual(saved[key], defaults[key])).length;
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

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      draftReady = false;
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
      savedDraft = captureDraft();
      activeDraftKey = systemDraftKey;
      hydratingDraft = true;
      applyDraft(readSessionDraft<SystemSettingsDraft>(systemDraftKey, systemDraftVersion));
      hydratingDraft = false;
      draftReady = true;
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
    const nextErrors: Record<string, string> = {};
    const corsOrigins = parseLineList(corsAllowedOriginsText);
    const allowedMimeTypes = parseLineList(allowedMimeTypesText);

    if (!runtime.default_provider.trim()) {
      nextErrors['runtime.default_provider'] = 'Select a gateway provider.';
    }
    if (!runtime.internal_default_provider.trim()) {
      nextErrors['runtime.internal_default_provider'] = 'Select an internal provider.';
    }
    if (!isPositiveInteger(runtime.conversation_timeout_seconds)) {
      nextErrors['runtime.conversation_timeout_seconds'] = 'Use a positive integer.';
    }
    if (!isPositiveInteger(runtime.provider_timeout_seconds)) {
      nextErrors['runtime.provider_timeout_seconds'] = 'Use a positive integer.';
    }
    if (!isNonNegativeInteger(runtime.message_threshold)) {
      nextErrors['runtime.message_threshold'] = 'Use a non-negative integer.';
    }
    if (!isNonNegativeInteger(runtime.token_threshold)) {
      nextErrors['runtime.token_threshold'] = 'Use a non-negative integer.';
    }
    if (!isPositiveInteger(runtime.max_context_tokens)) {
      nextErrors['runtime.max_context_tokens'] = 'Use a positive integer.';
    }
    if (!isPositiveInteger(runtime.max_injection_tokens)) {
      nextErrors['runtime.max_injection_tokens'] = 'Use a positive integer.';
    }
    if (!isPositiveInteger(runtime.rerank_candidate_multiplier)) {
      nextErrors['runtime.rerank_candidate_multiplier'] = 'Use a positive integer.';
    }
    if (!isPositiveInteger(runtime.rerank_min_candidates)) {
      nextErrors['runtime.rerank_min_candidates'] = 'Use a positive integer.';
    }
    if (!isPositiveInteger(runtime.rerank_max_candidates)) {
      nextErrors['runtime.rerank_max_candidates'] = 'Use a positive integer.';
    }
    if (runtime.rerank_max_candidates < runtime.rerank_min_candidates) {
      nextErrors['runtime.rerank_max_candidates'] = 'Max candidates must be greater than or equal to min candidates.';
    }
    if (!isPositiveInteger(runtime.memory_relation_max_hops)) {
      nextErrors['runtime.memory_relation_max_hops'] = 'Use a positive integer.';
    }
    if (!isNumberInRange(runtime.memory_relation_hop_decay_lambda, 0, 2)) {
      nextErrors['runtime.memory_relation_hop_decay_lambda'] = 'Use a value between 0 and 2.';
    }
    if (!isNumberInRange(runtime.memory_relation_hop_decay_exponent, 0.5, 3)) {
      nextErrors['runtime.memory_relation_hop_decay_exponent'] = 'Use a value between 0.5 and 3.';
    }
    if (!isNonNegativeInteger(runtime.rate_limit_rps)) {
      nextErrors['runtime.rate_limit_rps'] = 'Use a non-negative integer.';
    }
    if (!isNonNegativeInteger(runtime.rate_limit_burst)) {
      nextErrors['runtime.rate_limit_burst'] = 'Use a non-negative integer.';
    }
    if (!isNonNegativeInteger(runtime.cache_ttl_seconds)) {
      nextErrors['runtime.cache_ttl_seconds'] = 'Use a non-negative integer.';
    }
    if (corsOrigins.some((origin) => !isOriginValue(origin))) {
      nextErrors['runtime.cors_allowed_origins'] = 'Each origin must be an explicit http(s) origin.';
    }
    if (!runtime.multimodal_storage_backend.trim()) {
      nextErrors['runtime.multimodal_storage_backend'] = 'Choose a storage backend.';
    }
    if (runtime.multimodal_storage_backend === 'local' && !runtime.multimodal_storage_local_root.trim()) {
      nextErrors['runtime.multimodal_storage_local_root'] = 'Local storage needs a blob root path.';
    }
    if (!isPositiveInteger(runtime.multimodal_max_asset_bytes)) {
      nextErrors['runtime.multimodal_max_asset_bytes'] = 'Use a positive integer.';
    }
    if (allowedMimeTypes.some((mime) => !isMimeType(mime))) {
      nextErrors['runtime.multimodal_allowed_mime_types'] = 'Each value must be a valid MIME type like image/png.';
    }

    fieldErrors = nextErrors;
    if (Object.keys(nextErrors).length > 0) {
      error = 'Please fix the highlighted runtime fields.';
      success = '';
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const payload = await saveConfig($selectedTarget, {
        runtime: {
          ...runtime,
          cors_allowed_origins: corsOrigins,
          multimodal_allowed_mime_types: allowedMimeTypes
        }
      });
      applyConfig(payload);
      savedDraft = captureDraft();
      if (activeDraftKey) {
        clearSessionDraft(activeDraftKey);
      }
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
    fieldErrors = {};
    if (!isPostgresURL(databaseInput.trim())) {
      fieldErrors = { 'database.url': 'Database URL must use postgres:// or postgresql://.' };
      error = 'Please fix the database connection URL.';
      success = '';
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
      savedDraft = captureDraft();
      if (activeDraftKey) {
        clearSessionDraft(activeDraftKey);
      }
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
      savedDraft = captureDraft();
      if (activeDraftKey) {
        clearSessionDraft(activeDraftKey);
      }
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
      return `Draft ${draftValue}, Saved ${savedValue}`;
    }
    if (runtimeDiffers(key)) {
      return `Saved ${savedValue}, Live ${liveValue}`;
    }
    if (isCustomized(key)) {
      return `Saved ${savedValue}, Default ${defaultValue}`;
    }
    return `Draft ${draftValue}`;
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

  function systemStatusLine(): string {
    const overrideCount = countOverrides(runtimeSaved, runtimeDefaults);
    const dirtyCount = countDirty(runtime, runtimeSaved);
    const liveAppliedCount = countLiveApplied(runtime, runtimeSaved, runtimeDefaults, runtimeLive, runtimeMeta);
    const parts = [overrideCount > 0 ? `${overrideCount} overrides` : 'Runtime defaults active'];
    if (dirtyCount > 0) {
      parts.push(`${dirtyCount} draft`);
    }
    if (liveAppliedCount > 0) {
      parts.push(`${liveAppliedCount} live`);
    }
    if (ready?.status && ready.status !== 'unknown') {
      parts.push(`Health ${ready.status}`);
    }
    return parts.join(', ');
  }

  function requestConfirmation(
    title: string,
    description: string,
    subject: string,
    label: string,
    action: () => Promise<void>
  ): void {
    confirmTitle = title;
    confirmDescription = description;
    confirmSubject = subject;
    confirmLabel = label;
    pendingConfirmedAction = action;
    confirmOpen = true;
  }

  async function confirmPendingAction(): Promise<void> {
    const action = pendingConfirmedAction;
    if (!action) return;
    await action();
    confirmOpen = false;
    pendingConfirmedAction = null;
  }
</script>

<section class="page-stack">
  <PageHeader
    sticky
    eyebrow="Administer"
    title="System"
    description="Runtime configuration — provider defaults, timeouts, thresholds, rerank, relations, CORS, and multimodal policy."
    subtitle={systemStatusLine()}
  >
    <div slot="actions">
      {#if $targetState.session.authenticated && !loading}
        <div class="inline-group">
          <Button
            class="btn-secondary btn-sm"
            type="button"
            disabled={saving}
            onclick={() => requestConfirmation(
              'Reset all runtime overrides?',
              'Every stored runtime override will be cleared back to its deployment default.',
              `${countDirty(runtime, runtimeSaved)} draft, ${countOverrides(runtimeSaved, runtimeDefaults)} active overrides`,
              'Reset all overrides',
              resetAll
            )}
          >Reset all overrides</Button>
          <Button class="btn-primary" type="button" disabled={saving} onclick={persist}>
            {saving ? 'Saving…' : 'Save system settings'}
          </Button>
        </div>
      {/if}
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect or modify system settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading system settings…</div>
  {:else}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}

    <div class="system-workspace">
      <SettingsNavigation items={sections} />
      <div class="settings-sheet" data-scrolled="false" use:observeSheetMask>
        <div class="settings-scroll-mask" aria-hidden="true"></div>
        <SettingsSection id="system-traffic" title="Traffic" description="Provider defaults and request thresholds.">
<div class="form-grid two-up mobile-pairs">
            <SelectField
              label="Gateway default provider"
              items={llmProviderItems}
              bind:value={runtime.default_provider}
              error={runtimeFieldError('default_provider')}
            />
            <SelectField
              label="Internal default provider"
              items={llmProviderItems}
              bind:value={runtime.internal_default_provider}
              error={runtimeFieldError('internal_default_provider')}
            />
            <TextInputField label="Conversation timeout (s)" bind:value={runtime.conversation_timeout_seconds} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('conversation_timeout_seconds')} />
            <TextInputField label="Provider timeout (s)" bind:value={runtime.provider_timeout_seconds} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('provider_timeout_seconds')} />
            <TextInputField label="Message threshold" bind:value={runtime.message_threshold} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('message_threshold')} />
            <TextInputField label="Token threshold" bind:value={runtime.token_threshold} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('token_threshold')} />
          </div>
        </SettingsSection>
        <SettingsSection id="system-recall" title="Recall" description="Vector store &amp; injection budget.">
<div class="form-grid two-up mobile-pairs">
            <SelectField label="Vector store backend" items={vectorStoreItems} bind:value={runtime.vector_store_backend} />
            <TextInputField label="Max context tokens" bind:value={runtime.max_context_tokens} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('max_context_tokens')} />
            <TextInputField label="Max injection tokens" bind:value={runtime.max_injection_tokens} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('max_injection_tokens')} />
            <TextInputField label="Cache TTL (s)" bind:value={runtime.cache_ttl_seconds} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('cache_ttl_seconds')} />
          </div>
        </SettingsSection>
        <SettingsSection id="system-rerank" title="Rerank" description="Choose a relevance model, then set the candidate budget.">
<div class="form-grid two-up mobile-pairs">
            <RerankModelField bind:value={runtime.rerank_model} error={runtimeFieldError('rerank_model')} />
            <TextInputField label="Candidate multiplier" bind:value={runtime.rerank_candidate_multiplier} type="number" fieldClass="compact-field" min="1" max="16" error={runtimeFieldError('rerank_candidate_multiplier')} />
            <TextInputField label="Min rerank candidates" bind:value={runtime.rerank_min_candidates} type="number" fieldClass="compact-field" min="1" error={runtimeFieldError('rerank_min_candidates')} />
            <TextInputField label="Max rerank candidates" bind:value={runtime.rerank_max_candidates} type="number" fieldClass="compact-field" min="1" error={runtimeFieldError('rerank_max_candidates')} />
          </div>
        </SettingsSection>
        <SettingsSection id="system-relations" title="Relations" description="Hop graph decay settings.">
<div class="form-grid two-up mobile-pairs">
            <TextInputField label="Relation max hops" bind:value={runtime.memory_relation_max_hops} type="number" fieldClass="compact-field" min="1" max="4" error={runtimeFieldError('memory_relation_max_hops')} />
            <TextInputField label="Hop decay lambda" bind:value={runtime.memory_relation_hop_decay_lambda} type="number" fieldClass="compact-field" min="0" max="2" step="0.05" error={runtimeFieldError('memory_relation_hop_decay_lambda')} />
            <TextInputField label="Hop decay exponent" bind:value={runtime.memory_relation_hop_decay_exponent} type="number" fieldClass="compact-field" min="0.5" max="3" step="0.05" error={runtimeFieldError('memory_relation_hop_decay_exponent')} />
          </div>
        </SettingsSection>
        <SettingsSection id="system-web" title="Web" description="Rate limits and cross-origin policy.">
<div class="form-grid two-up mobile-pairs">
          <TextInputField label="Rate limit RPS" bind:value={runtime.rate_limit_rps} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('rate_limit_rps')} />
          <TextInputField label="Rate limit burst" bind:value={runtime.rate_limit_burst} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('rate_limit_burst')} />
          <SwitchField
            label="Cross-site admin cookie"
            bind:checked={runtime.session_cookie_cross_site}
          />
          <TextareaField
            label="CORS allowed origins"
            bind:value={corsAllowedOriginsText}
            rows={4}
            placeholder="Leave empty to reflect any incoming Origin&#10;https://manager.example.com"
            error={runtimeFieldError('cors_allowed_origins')}
            fieldClass="field-span-2"
          />
        </div>
        </SettingsSection>
        <SettingsSection id="system-multimodal" title="Multimodal" description="Blob storage, extractors, and content policy.">
<div class="system-subsection">
          <span class="sidebar-label">Storage</span>
          <div class="form-grid two-up mobile-pairs">
            <SwitchField label="Multimodal ingestion" bind:checked={runtime.multimodal_enabled} />
            <SelectField
              label="Blob storage backend"
              items={storageBackendItems}
              bind:value={runtime.multimodal_storage_backend}
              error={runtimeFieldError('multimodal_storage_backend')}
            />
            <TextInputField label="Local blob root" bind:value={runtime.multimodal_storage_local_root} error={runtimeFieldError('multimodal_storage_local_root')} />
            <TextInputField label="Remote bucket" bind:value={runtime.multimodal_storage_bucket} placeholder="memstation-assets" />
          </div>
        </div>

        <div class="system-subsection">
          <span class="sidebar-label">Extractors</span>
          <div class="form-grid two-up mobile-pairs">
            <TextInputField label="Image extractor model" bind:value={runtime.multimodal_extract_image_model} placeholder="configured vision model" />
            <TextInputField label="Audio extractor model" bind:value={runtime.multimodal_extract_audio_model} placeholder="configured ASR model" />
            <TextInputField label="Video extractor model" bind:value={runtime.multimodal_extract_video_model} placeholder="configured video model" />
            <TextInputField label="File extractor model" bind:value={runtime.multimodal_extract_file_model} placeholder="configured document model" />
          </div>
        </div>

        <div class="system-subsection">
          <span class="sidebar-label">Policies</span>
          <div class="form-grid two-up mobile-pairs">
            <TextInputField label="Max asset bytes" bind:value={runtime.multimodal_max_asset_bytes} type="number" fieldClass="compact-field" min="0" error={runtimeFieldError('multimodal_max_asset_bytes')} />
            <SwitchField label="Audio transcription" bind:checked={runtime.multimodal_audio_transcription_enabled} />
            <SwitchField label="Video frame sampling" bind:checked={runtime.multimodal_video_frame_sampling_enabled} />
            <TextareaField
              label="Allowed MIME types"
              bind:value={allowedMimeTypesText}
              rows={4}
              placeholder="image/png&#10;application/pdf&#10;audio/mpeg"
              error={runtimeFieldError('multimodal_allowed_mime_types')}
              fieldClass="field-span-2"
            />
          </div>
        </div>
        </SettingsSection>
        <SettingsSection id="system-database" title="Database connection" description={`${database.status}, ${databaseSourceLabel(database.source)}. Applied separately.`}>
<div class="database-summary">
          <strong class="mono">{database.display ?? 'No database connection is configured.'}</strong>
          {#if database.updated_at}
            <span class="helper-text">Updated {new Date(database.updated_at).toLocaleString()}</span>
          {/if}
        </div>
        {#if database.last_error}
          <div class="error-state">{database.last_error}</div>
        {/if}

        <TextInputField
          label="Connection URI"
          bind:value={databaseInput}
          placeholder="postgres://user:password@127.0.0.1:5432/memstation?sslmode=disable"
          error={databaseFieldError('url')}
        />
        <div class="inline-group" style="justify-content: flex-end;">
          <Button
            class="btn-secondary btn-sm"
            type="button"
            disabled={databaseSaving || database.source !== 'override'}
            onclick={() => requestConfirmation(
              'Clear database override?',
              'The target will return to its deployment-level database connection after this change.',
              database.display ?? 'database override',
              'Clear override',
              clearDatabase
            )}
          >
            Clear override
          </Button>
          <Button class="btn-primary btn-sm" type="button" disabled={databaseSaving || !databaseInput.trim()} onclick={persistDatabase}>
            {databaseSaving ? 'Applying…' : 'Apply database'}
          </Button>
        </div>
        </SettingsSection>
        <SettingsSection id="system-overrides" title="Overrides &amp; drift" description="Fields deviating from deployment defaults or the live runtime.">
{#if trackedRuntimeKeys().length === 0}
          <div class="empty-state">No overrides — system is using deployment defaults.</div>
        {:else}
          <div class="runtime-field-list">
            {#each trackedRuntimeKeys() as key}
              <div class="runtime-field-row">
                <div>
                  <strong>{displayKeyName(key)}</strong>
                  <div class="helper-text">{summaryTextFor(key)}</div>
                </div>
                <div class="runtime-field-status">
                  {#if draftDiffers(key)}
                    <Button class="btn-ghost btn-sm" type="button" disabled={saving} onclick={() => revertDraft(key)}>Revert</Button>
                  {/if}
                  {#if isCustomized(key)}
                    <Button
                      class="btn-ghost btn-sm"
                      type="button"
                      disabled={saving}
                      onclick={() => requestConfirmation(
                        'Clear runtime override?',
                        'This field will return to its deployment default.',
                        key,
                        'Clear override',
                        () => resetField(key)
                      )}
                    >Clear</Button>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}
        </SettingsSection>
      </div>
    </div>
  {/if}
</section>

<ConfirmActionDialog
  bind:open={confirmOpen}
  title={confirmTitle}
  description={confirmDescription}
  subject={confirmSubject}
  confirmLabel={confirmLabel}
  busy={saving || databaseSaving}
  on:cancel={() => (pendingConfirmedAction = null)}
  on:confirm={confirmPendingAction}
/>

<style>
  :global(html:has(.system-workspace)) { scroll-behavior: smooth; }
  .system-workspace { display: grid; grid-template-columns: 148px minmax(0, 1fr); gap: 24px; align-items: start; }
  .settings-scroll-mask { position: sticky; top: calc(var(--page-anchor-offset) - 12px); height: 36px; margin-bottom: -36px; background: linear-gradient(to bottom, var(--surface), transparent); pointer-events: none; opacity: 0; z-index: 2; }
  .settings-sheet:global([data-scrolled='true']) .settings-scroll-mask { opacity: 1; }
  .settings-sheet { min-width: 0; border: 1px solid var(--border); border-radius: var(--radius-lg); background: var(--surface); overflow: clip; }

  .system-subsection {
    display: grid;
    gap: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
  }

  .system-subsection:first-child {
    padding-top: 0;
    border-top: none;
  }

  .database-summary {
    min-width: 0;
    overflow-wrap: anywhere;
    display: grid;
    gap: 3px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
  }

  @media (max-width: 900px) {
    .system-workspace { grid-template-columns: 1fr; gap: 16px; }
  }
  @media (max-width: 640px) { .settings-scroll-mask { top: calc(var(--topbar-height) + var(--shell-inset)); } }
</style>
