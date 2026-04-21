<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, saveConfig } from '$lib/api/client';
  import SwitchField from '$lib/components/SwitchField.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { isNonNegativeInteger } from '$lib/forms/validation';
  import {
    buildSessionDraftKey,
    clearSessionDraft,
    draftsEqual,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '$lib/stores/session-drafts';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { ProviderSetPayload, ProviderSummary } from '$lib/types';

  type ProviderScope = 'gateway' | 'internal' | 'embedding' | 'rerank';
  type ModelSettingsDraft = {
    chatModel: string;
    summaryModel: string;
    analysisModel: string;
    embeddingModel: string;
    toolWriteEnabled: boolean;
    maxInjectionTokens: number;
  };

  const modelDraftVersion = 1;

  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let lastLoadKey = '';
  let payload: Record<string, unknown> = {};
  let chatModel = '';
  let summaryModel = '';
  let analysisModel = '';
  let embeddingModel = '';
  let toolWriteEnabled = false;
  let maxInjectionTokens = 0;
  let fieldErrors: Record<string, string> = {};
  let activeDraftKey = '';
  let draftReady = false;
  let hydratingDraft = false;
  let savedDraft = emptyModelDraft();
  let currentDraft = emptyModelDraft();

  const providerScopeOrder: ProviderScope[] = ['gateway', 'internal', 'embedding', 'rerank'];

  onMount(async () => {
    await loadConfig();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: modelDraftKey = buildSessionDraftKey('settings.models', $selectedTarget?.id);
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadConfig();
  }
  $: currentDraft = {
    chatModel,
    summaryModel,
    analysisModel,
    embeddingModel,
    toolWriteEnabled,
    maxInjectionTokens
  };
  $: if (draftReady && activeDraftKey && !hydratingDraft) {
    if (draftsEqual(currentDraft, savedDraft)) {
      clearSessionDraft(activeDraftKey);
      } else {
        writeSessionDraft(activeDraftKey, modelDraftVersion, currentDraft);
    }
  }

  useSessionDraftUnloadWarning(
    () => draftReady && !draftsEqual(currentDraft, savedDraft),
    'These model settings have unsaved changes. Closing or reloading the page will discard the draft kept in this tab.'
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
    writeSessionDraft(activeDraftKey, modelDraftVersion, snapshot);
  });

  function emptyModelDraft(): ModelSettingsDraft {
    return {
      chatModel: '',
      summaryModel: '',
      analysisModel: '',
      embeddingModel: '',
      toolWriteEnabled: false,
      maxInjectionTokens: 0
    };
  }

  function captureDraft(): ModelSettingsDraft {
    return {
      chatModel,
      summaryModel,
      analysisModel,
      embeddingModel,
      toolWriteEnabled,
      maxInjectionTokens
    };
  }

  function applyDraft(draft: ModelSettingsDraft | null): void {
    if (!draft) {
      return;
    }
    chatModel = draft.chatModel;
    summaryModel = draft.summaryModel;
    analysisModel = draft.analysisModel;
    embeddingModel = draft.embeddingModel;
    toolWriteEnabled = draft.toolWriteEnabled;
    maxInjectionTokens = draft.maxInjectionTokens;
  }

  async function loadConfig(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      draftReady = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      payload = await fetchConfig($selectedTarget);
      chatModel = String(payload.chat_model ?? '');
      summaryModel = String(payload.summary_model ?? '');
      analysisModel = String(payload.analysis_model ?? '');
      embeddingModel = String(payload.embedding_model ?? '');
      toolWriteEnabled = Boolean(payload.tool_write_enabled);
      maxInjectionTokens = Number(payload.max_injection_tokens ?? 0);
      savedDraft = captureDraft();
      activeDraftKey = modelDraftKey;
      hydratingDraft = true;
      applyDraft(readSessionDraft<ModelSettingsDraft>(modelDraftKey, modelDraftVersion));
      hydratingDraft = false;
      draftReady = true;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load config';
    } finally {
      loading = false;
    }
  }

  async function persist(): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    fieldErrors = {};
    if (!isNonNegativeInteger(maxInjectionTokens)) {
      fieldErrors.maxInjectionTokens = 'Use a non-negative integer.';
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      payload = {
        ...payload,
        chat_model: chatModel,
        summary_model: summaryModel,
        analysis_model: analysisModel,
        embedding_model: embeddingModel,
        tool_write_enabled: toolWriteEnabled,
        max_injection_tokens: maxInjectionTokens
      };
      payload = await saveConfig($selectedTarget, payload);
      chatModel = String(payload.chat_model ?? '');
      summaryModel = String(payload.summary_model ?? '');
      analysisModel = String(payload.analysis_model ?? '');
      embeddingModel = String(payload.embedding_model ?? '');
      toolWriteEnabled = Boolean(payload.tool_write_enabled);
      maxInjectionTokens = Number(payload.max_injection_tokens ?? 0);
      savedDraft = captureDraft();
      if (activeDraftKey) {
        clearSessionDraft(activeDraftKey);
      }
      success = 'Configuration saved to the selected target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save config';
    } finally {
      saving = false;
    }
  }

  function normalizeProviderSets(configPayload: Record<string, unknown>): Record<ProviderScope, ProviderSummary[]> {
    const sets = (configPayload.provider_sets ?? {}) as ProviderSetPayload;
    return {
      gateway: sets.gateway ?? ((configPayload.providers as ProviderSummary[] | undefined) ?? []),
      internal: sets.internal ?? [],
      embedding: sets.embedding ?? [],
      rerank: sets.rerank ?? []
    };
  }

  function scopeTitle(scope: ProviderScope): string {
    switch (scope) {
      case 'gateway':
        return 'Gateway';
      case 'internal':
        return 'Internal';
      case 'embedding':
        return 'Embedding';
      case 'rerank':
        return 'Rerank';
    }
  }

  $: providerSets = normalizeProviderSets(payload);
  $: providerScopeCards = providerScopeOrder.map((scope) => ({
    scope,
    title: scopeTitle(scope),
    providers: providerSets[scope] ?? [],
    defaultProvider:
      scope === 'gateway' ? String(payload.default_provider ?? '') :
      scope === 'internal' ? String(payload.internal_default_provider ?? '') :
      ''
  }));

  function scopeSummary(scope: ProviderScope): string {
    if (scope === 'embedding' || scope === 'rerank') {
      return 'Selection follows the configured model namespace.';
    }
    return 'Requests route to the selected default provider.';
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings</span>
    <h1 class="page-title">Models</h1>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to modify runtime settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading config...</div>
  {:else}
    <div class="page-columns">
      <div class="page-column">
        <section class="card page-stack">
          <div class="page-header">
            <div>
              <h2 class="page-title" style="font-size: 1.08rem;">Model Defaults</h2>
            </div>
          </div>

          <div class="form-grid two-up">
            <TextInputField label="Chat Model" bind:value={chatModel} />
            <TextInputField label="Summary Model" bind:value={summaryModel} />
            <TextInputField label="Analysis Model" bind:value={analysisModel} />
            <TextInputField label="Embedding Model" bind:value={embeddingModel} />
            <SwitchField
              label="Tool Writes"
              help="Allow model-triggered memory writes for users inheriting the deployment default."
              bind:checked={toolWriteEnabled}
            />
            <TextInputField
              label="Max Injection Tokens"
              bind:value={maxInjectionTokens}
              type="number"
              min="0"
              error={fieldErrors.maxInjectionTokens}
            />
          </div>

          <div class="inline-group" style="justify-content: flex-end; margin-top: 16px;">
            <button class="button" type="button" disabled={saving} on:click={persist}>{saving ? 'Saving...' : 'Save Settings'}</button>
          </div>
          {#if success}
            <div class="notice-card">{success}</div>
          {/if}
        </section>
      </div>

      <div class="page-column">
        <section class="card page-stack">
          <div class="page-header">
            <div>
              <h2 class="page-title" style="font-size: 1.08rem;">Provider Routes</h2>
            </div>
            <a class="button-secondary" href="/settings/providers">Open provider settings</a>
          </div>

          <div class="toolbar">
            {#each providerScopeCards as card}
              <div class="route-row">
                <div class="route-row-header">
                  <strong>{card.title}</strong>
                  <span class="subtle-text">{card.providers.length} Ready</span>
                </div>
                <div class="subtle-text">{scopeSummary(card.scope)}</div>
                {#if card.defaultProvider}
                  <div class="helper-text">Default route: {card.defaultProvider}</div>
                {/if}
                <div class="route-provider-list">
                  {#if card.providers.length > 0}
                    {#each card.providers as provider}
                      <div class="route-provider-item">
                        <span>{provider.provider}</span>
                        <span class="subtle-text">{provider.has_api_key ? 'ready' : 'needs key'}</span>
                      </div>
                    {/each}
                  {:else}
                    <span class="subtle-text">No providers available.</span>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        </section>
      </div>
    </div>
  {/if}
</section>

<style>
  .route-row {
    display: grid;
    gap: 10px;
    padding: 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: rgba(255, 255, 255, 0.86);
  }

  .route-row-header,
  .route-provider-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .route-provider-list {
    display: grid;
    gap: 8px;
  }
</style>
