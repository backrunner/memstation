<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, saveConfig } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';

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

  onMount(async () => {
    await loadConfig();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadConfig();
  }

  async function loadConfig(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
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
      success = 'Configuration saved to the selected target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save config';
    } finally {
      saving = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings</span>
    <h1 class="page-title">Models, thresholds, and provider overrides</h1>
    <p class="page-description">
      Adjust user-facing runtime defaults without turning the manager into a proxy. Changes are written directly to the selected MemStation target over admin REST.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to modify runtime settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading config...</div>
  {:else}
    <div class="grid-2">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Primary settings</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Model routing defaults</h2>
          </div>
        </div>

        <div class="form-grid two-up">
          <label class="field">
            <span class="field-label">Chat model</span>
            <input class="input" bind:value={chatModel} />
          </label>
          <label class="field">
            <span class="field-label">Summary model</span>
            <input class="input" bind:value={summaryModel} />
          </label>
          <label class="field">
            <span class="field-label">Analysis model</span>
            <input class="input" bind:value={analysisModel} />
          </label>
          <label class="field">
            <span class="field-label">Embedding model</span>
            <input class="input" bind:value={embeddingModel} />
          </label>
          <label class="field">
            <span class="field-label">Tool writes enabled</span>
            <div class="inline-group">
              <button class={toolWriteEnabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (toolWriteEnabled = true)}>Enabled</button>
              <button class={!toolWriteEnabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (toolWriteEnabled = false)}>Disabled</button>
            </div>
          </label>
          <label class="field">
            <span class="field-label">Max injection tokens</span>
            <input class="input" type="number" bind:value={maxInjectionTokens} />
          </label>
        </div>

        <div class="inline-group" style="justify-content: flex-end; margin-top: 16px;">
          <button class="button" type="button" disabled={saving} on:click={persist}>{saving ? 'Saving...' : 'Save settings'}</button>
        </div>
        {#if success}
          <div class="notice-card" style="margin-top: 12px;">{success}</div>
        {/if}
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Provider state</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Current provider set summary</h2>
          </div>
        </div>
        <JsonView value={payload.provider_sets ?? payload.providers ?? []} emptyLabel="No provider summary returned." />
      </section>
    </div>
  {/if}
</section>
