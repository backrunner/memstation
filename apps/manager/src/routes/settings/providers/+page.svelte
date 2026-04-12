<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, fetchProviderDiagnostics, saveConfig } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AdminConfigPayload, ProviderDiagnostic, ProviderSetPayload, ProviderSummary } from '$lib/types';

  type ProviderScope = 'gateway' | 'internal' | 'embedding' | 'rerank';

  type ProviderForm = {
    scope: ProviderScope;
    provider: string;
    baseUrl: string;
    apiKey: string;
    hasAPIKey: boolean;
    source: string;
    updatedAt?: string;
  };

  const scopeOrder: ProviderScope[] = ['gateway', 'internal', 'embedding', 'rerank'];

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

  function normalizeProviderSets(configPayload: AdminConfigPayload | null): Record<ProviderScope, ProviderSummary[]> {
    const sets = (configPayload?.provider_sets ?? {}) as ProviderSetPayload;
    return {
      gateway: sets.gateway ?? ((configPayload?.providers as ProviderSummary[] | undefined) ?? []),
      internal: sets.internal ?? [],
      embedding: sets.embedding ?? [],
      rerank: sets.rerank ?? []
    };
  }

  function toForms(providerSets: Record<ProviderScope, ProviderSummary[]>): ProviderForm[] {
    const next: ProviderForm[] = [];
    for (const scope of scopeOrder) {
      for (const provider of providerSets[scope] ?? []) {
        next.push({
          scope,
          provider: provider.provider,
          baseUrl: provider.base_url,
          apiKey: '',
          hasAPIKey: provider.has_api_key,
          source: provider.source,
          updatedAt: provider.updated_at
        });
      }
    }
    return next;
  }

  function formsForScope(scope: ProviderScope): ProviderForm[] {
    return forms.filter((form) => form.scope === scope);
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
      forms = toForms(normalizeProviderSets(config));
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
      const currentSets = normalizeProviderSets(config);
      const nextSets: Record<string, Record<string, Record<string, string>>> = {};

      for (const scope of scopeOrder) {
        const scopePayload: Record<string, Record<string, string>> = {};
        const currentProviders = (currentSets[scope] ?? []).reduce<Record<string, ProviderSummary>>((acc, item) => {
          acc[item.provider] = item;
          return acc;
        }, {});
        for (const form of formsForScope(scope)) {
          const current = currentProviders[form.provider];
          const payload: Record<string, string> = {};
          if (form.baseUrl.trim() !== (current?.base_url ?? '')) {
            payload.base_url = form.baseUrl.trim();
          }
          if (form.apiKey.trim()) {
            payload.api_key = form.apiKey.trim();
          }
          if (Object.keys(payload).length > 0) {
            scopePayload[form.provider] = payload;
          }
        }
        if (Object.keys(scopePayload).length > 0) {
          nextSets[scope] = scopePayload;
        }
      }

      config = await saveConfig($selectedTarget, {
        provider_sets: nextSets
      });
      diagnostics = await fetchProviderDiagnostics($selectedTarget);
      forms = toForms(normalizeProviderSets(config));
      success = 'Provider set overrides saved. Sensitive API keys are not echoed back by the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save provider settings';
    } finally {
      saving = false;
    }
  }

  function diagnosticFor(scope: ProviderScope, provider: string): ProviderDiagnostic | undefined {
    return diagnostics.find((item) => item.scope === scope && item.provider === provider);
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

  function scopeTitle(scope: ProviderScope): string {
    switch (scope) {
      case 'gateway':
        return 'Gateway proxy providers';
      case 'internal':
        return 'Internal LLM providers';
      case 'embedding':
        return 'Embedding providers';
      case 'rerank':
        return 'Reranker providers';
    }
  }

  function scopeDescription(scope: ProviderScope): string {
    switch (scope) {
      case 'gateway':
        return 'External proxy traffic.';
      case 'internal':
        return 'Internal model tasks.';
      case 'embedding':
        return 'Embedding-only credentials.';
      case 'rerank':
        return 'Candidate reranking.';
    }
  }

  function routeLabel(diagnostic?: ProviderDiagnostic): string {
    if (diagnostic?.is_desired_default && diagnostic?.is_live_default) {
      return 'active default';
    }
    if (diagnostic?.is_desired_default) {
      return diagnostic.restart_required ? 'default after restart' : 'desired default';
    }
    if (diagnostic?.is_live_default) {
      return 'live default';
    }
    return '';
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings / Providers</span>
    <h1 class="page-title">Manage gateway, internal, embedding, and rerank provider sets</h1>
    <p class="page-description">Rotate provider endpoints and encrypted credentials for the selected target.</p>
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
        <span class="kpi-meta">Ready now.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Warnings</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.status === 'warning').length}</span>
        <span class="kpi-meta">Need review.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Degraded</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.status === 'degraded').length}</span>
        <span class="kpi-meta">Blocking issues.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Restart Pending</span>
        <span class="kpi-value">{diagnostics.filter((item) => item.restart_required).length}</span>
        <span class="kpi-meta">Pending live sync.</span>
      </div>
    </div>

    {#each scopeOrder as scope}
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">{scope}</div>
            <h2 class="page-title" style="font-size: 1.35rem;">{scopeTitle(scope)}</h2>
            <p class="page-description">{scopeDescription(scope)}</p>
          </div>
        </div>

        <div class="provider-card-grid">
          {#each formsForScope(scope) as form}
            {@const diagnostic = diagnosticFor(form.scope, form.provider)}
            <section class="card">
              <div class="page-header">
                <div>
                  <div class="sidebar-label">{form.provider}</div>
                  <h3 class="page-title" style="font-size: 1.15rem; text-transform: capitalize;">{form.provider}</h3>
                </div>
                <div class="toolbar" style="gap: 8px;">
                  <StatusBadge tone={toneFor(diagnostic?.status)} label={diagnostic?.status ?? 'unknown'} />
                  {#if form.source === 'override'}
                    <StatusBadge tone="brand" label="override" />
                  {/if}
                </div>
              </div>

              <div class="toolbar">
                <div class="provider-summary">
                  <span>{form.hasAPIKey ? 'Stored key' : 'No key'}</span>
                  {#if routeLabel(diagnostic)}
                    <span>{routeLabel(diagnostic)}</span>
                  {/if}
                  {#if diagnostic?.restart_required}
                    <span>restart pending</span>
                  {/if}
                </div>
                <label class="field">
                  <span class="field-label">Base URL</span>
                  <input class="input" bind:value={form.baseUrl} placeholder="https://api.example.com/v1" />
                </label>
                <label class="field">
                  <span class="field-label">Rotate API key</span>
                  <input class="input" bind:value={form.apiKey} type="password" autocomplete="off" placeholder="Leave blank to keep existing secret" />
                </label>
                <div class="target-preview">
                  <div class="target-preview-meta">
                    <span>{diagnostic?.host ?? 'no host'}</span>
                    <span>{diagnostic?.scheme ?? 'n/a'}</span>
                    <span>{diagnostic?.live_timeout_seconds ?? 0}s live</span>
                  </div>
                  {#if (diagnostic?.issues.length ?? 0) > 0}
                    <div class="helper-text">{diagnostic?.issues?.[0]}</div>
                  {/if}
                </div>
                <div class="helper-text">{form.updatedAt ? new Date(form.updatedAt).toLocaleString() : 'Using seeded defaults'}</div>
              </div>
            </section>
          {/each}
        </div>
      </section>
    {/each}

    <div class="inline-group" style="justify-content: flex-end;">
      <button class="button" type="button" disabled={saving} on:click={persist}>{saving ? 'Saving...' : 'Save provider set overrides'}</button>
    </div>
  {/if}
</section>

<style>
  .provider-card-grid {
    display: grid;
    gap: 16px;
    align-items: start;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .provider-summary {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    color: var(--text-secondary);
    font-size: 0.9rem;
  }

  .provider-summary span {
    padding: 4px 10px;
    border-radius: 999px;
    background: rgba(15, 94, 168, 0.08);
  }

  .target-preview {
    display: grid;
    gap: 10px;
    padding: 14px 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
  }

  .target-preview-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    color: var(--text-secondary);
    font-size: 0.9rem;
  }

  .target-preview-meta span {
    padding: 4px 10px;
    border-radius: 999px;
    background: rgba(15, 94, 168, 0.08);
  }

  @media (min-width: 1720px) {
    .provider-card-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }

  @media (max-width: 1120px) {
    .provider-card-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
