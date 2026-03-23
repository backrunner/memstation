<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, fetchProviderDiagnostics, saveConfig } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AdminConfigPayload, ProviderDiagnostic, ProviderSummary } from '$lib/types';

  type ProviderForm = {
    provider: string;
    baseUrl: string;
    apiKey: string;
    hasAPIKey: boolean;
    source: string;
    updatedAt?: string;
  };

  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let lastLoadKey = '';
  let config: AdminConfigPayload | null = null;
  let forms: ProviderForm[] = [];
  let diagnostics: ProviderDiagnostic[] = [];

  onMount(async () => {
    await loadConfig();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadConfig();
  }

  function toForms(providers: ProviderSummary[]): ProviderForm[] {
    return providers.map((provider) => ({
      provider: provider.provider,
      baseUrl: provider.base_url,
      apiKey: '',
      hasAPIKey: provider.has_api_key,
      source: provider.source,
      updatedAt: provider.updated_at
    }));
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
      [config, diagnostics] = await Promise.all([
        fetchConfig($selectedTarget),
        fetchProviderDiagnostics($selectedTarget)
      ]);
      forms = toForms((config.providers as ProviderSummary[] | undefined) ?? []);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load provider settings';
    } finally {
      loading = false;
    }
  }

  async function persist(): Promise<void> {
    if (!$selectedTarget || !config) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const nextProviders: Record<string, Record<string, string>> = {};
      const currentProviders = ((config.providers as ProviderSummary[] | undefined) ?? []).reduce<Record<string, ProviderSummary>>((acc, item) => {
        acc[item.provider] = item;
        return acc;
      }, {});

      for (const form of forms) {
        const current = currentProviders[form.provider];
        const payload: Record<string, string> = {};
        if (form.baseUrl.trim() !== (current?.base_url ?? '')) {
          payload.base_url = form.baseUrl.trim();
        }
        if (form.apiKey.trim()) {
          payload.api_key = form.apiKey.trim();
        }
        if (Object.keys(payload).length > 0) {
          nextProviders[form.provider] = payload;
        }
      }

      config = await saveConfig($selectedTarget, {
        providers: nextProviders
      });
      diagnostics = await fetchProviderDiagnostics($selectedTarget);
      forms = toForms((config.providers as ProviderSummary[] | undefined) ?? []);
      success = 'Provider overrides saved. Sensitive API keys are not echoed back by the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save provider settings';
    } finally {
      saving = false;
    }
  }

  function diagnosticFor(provider: string): ProviderDiagnostic | undefined {
    return diagnostics.find((item) => item.provider === provider);
  }

  function toneFor(status?: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    switch (status) {
      case 'ready':
        return 'success';
      case 'degraded':
        return 'danger';
      case 'warning':
        return 'warning';
      default:
        return 'muted';
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings / Providers</span>
    <h1 class="page-title">Manage upstream provider endpoints and credential state</h1>
    <p class="page-description">
      Update provider base URLs or rotate credentials directly against the selected target. The manager never displays stored plaintext secrets.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to manage provider settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading provider settings...</div>
  {:else}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}

    <div class="kpi-grid">
      <div class="kpi-card">
        <span class="kpi-label">Ready Providers</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.status === 'ready').length}</span>
        <span class="kpi-meta">Providers with credentials and clean endpoint posture.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Warnings</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.status === 'warning').length}</span>
        <span class="kpi-meta">Providers with partial configuration or restart-only drift.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Degraded</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.status === 'degraded').length}</span>
        <span class="kpi-meta">Default-route risk requiring operator action.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Restart Pending</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.restart_required).length}</span>
        <span class="kpi-meta">Provider timeout drift not yet active in this process.</span>
      </div>
    </div>

    <div class="grid-3">
      {#each forms as form, index}
        {@const diagnostic = diagnosticFor(form.provider)}
        <section class="card">
          <div class="page-header">
            <div>
              <div class="sidebar-label">{form.provider}</div>
              <h2 class="page-title" style="font-size: 1.35rem; text-transform: capitalize;">{form.provider}</h2>
            </div>
            <div class="toolbar" style="gap: 8px;">
              <StatusBadge tone={toneFor(diagnostic?.status)} label={diagnostic?.status ?? 'unknown'} />
              <StatusBadge tone={form.hasAPIKey ? 'success' : 'warning'} label={form.hasAPIKey ? 'Key configured' : 'No key'} />
              <StatusBadge tone={form.source === 'override' ? 'brand' : 'muted'} label={form.source} />
            </div>
          </div>

          <div class="toolbar">
            <div class="runtime-field-status" style="justify-content: flex-start;">
              {#if diagnostic?.is_desired_default}
                <StatusBadge tone="brand" label="Desired default" />
              {/if}
              {#if diagnostic?.is_live_default}
                <StatusBadge tone="success" label="Live default" />
              {/if}
              {#if diagnostic?.restart_required}
                <StatusBadge tone="warning" label="Restart pending" />
              {/if}
            </div>
            <label class="field">
              <span class="field-label">Base URL</span>
              <input class="input" bind:value={forms[index].baseUrl} placeholder="https://api.example.com/v1" />
            </label>
            <label class="field">
              <span class="field-label">Rotate API key</span>
              <input class="input" bind:value={forms[index].apiKey} type="password" autocomplete="off" placeholder="Leave blank to keep existing secret" />
              <span class="field-help">Submitting a value writes a new encrypted credential and records an audit event.</span>
            </label>
            <div class="debug-block" style="background: var(--bg-panel-muted);">
              <div class="sidebar-label">Diagnostic posture</div>
              <div class="helper-text">Host: {diagnostic?.host ?? 'n/a'} · scheme: {diagnostic?.scheme ?? 'n/a'}</div>
              <div class="helper-text">
                timeout desired/live: {diagnostic?.desired_timeout_seconds ?? 0}s / {diagnostic?.live_timeout_seconds ?? 0}s
              </div>
              {#if (diagnostic?.issues.length ?? 0) > 0}
                <div class="toolbar" style="margin-top: 10px;">
                  {#each diagnostic?.issues ?? [] as issue}
                    <div class="notice-card" style="padding: 12px;">{issue}</div>
                  {/each}
                </div>
              {:else}
                <div class="helper-text" style="margin-top: 10px;">No provider posture issues detected on this target.</div>
              {/if}
            </div>
            <div class="helper-text">Last updated: {form.updatedAt ? new Date(form.updatedAt).toLocaleString() : 'Default runtime config'}</div>
          </div>
        </section>
      {/each}
    </div>

    <div class="inline-group" style="justify-content: flex-end;">
      <button class="button" type="button" disabled={saving} on:click={persist}>{saving ? 'Saving...' : 'Save provider overrides'}</button>
    </div>
  {/if}
</section>
