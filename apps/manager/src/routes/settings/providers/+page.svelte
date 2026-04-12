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

  function providerMeta(form: ProviderForm, diagnostic?: ProviderDiagnostic): string {
    const parts = [form.hasAPIKey ? 'stored key' : 'no key'];
    if (form.source === 'override') {
      parts.push('override');
    }
    if (diagnostic?.is_desired_default && diagnostic?.is_live_default) {
      parts.push('active default');
    } else if (diagnostic?.is_desired_default) {
      parts.push(diagnostic.restart_required ? 'default after restart' : 'desired default');
    } else if (diagnostic?.is_live_default) {
      parts.push('live default');
    }
    if (diagnostic?.restart_required) {
      parts.push('restart pending');
    }
    return parts.join(' · ');
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Settings</span>
    <h1 class="page-title">Provider sets</h1>
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

    <div class="summary-strip">
      <div class="summary-chip"><strong>{diagnostics.filter((item) => item.status === 'ready').length}</strong> ready</div>
      <div class="summary-chip"><strong>{diagnostics.filter((item) => item.status === 'warning').length}</strong> warnings</div>
      <div class="summary-chip"><strong>{diagnostics.filter((item) => item.status === 'degraded').length}</strong> degraded</div>
      <div class="summary-chip"><strong>{diagnostics.filter((item) => item.restart_required).length}</strong> restart pending</div>
    </div>

    {#each scopeOrder as scope}
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <h2 class="page-title" style="font-size: 1.35rem;">{scopeTitle(scope)}</h2>
          </div>
        </div>

        <div class="provider-card-grid">
          {#each formsForScope(scope) as form}
            {@const diagnostic = diagnosticFor(form.scope, form.provider)}
            <section class="card">
              <div class="page-header">
                <div>
                  <h3 class="page-title" style="font-size: 1.15rem; text-transform: capitalize;">{form.provider}</h3>
                  <div class="helper-text">{providerMeta(form, diagnostic)}</div>
                </div>
                <StatusBadge tone={toneFor(diagnostic?.status)} label={diagnostic?.status ?? 'unknown'} />
              </div>

              <div class="toolbar">
                <label class="field">
                  <span class="field-label">Base URL</span>
                  <input class="input" bind:value={form.baseUrl} placeholder="https://api.example.com/v1" />
                </label>
                <label class="field">
                  <span class="field-label">Rotate API key</span>
                  <input class="input" bind:value={form.apiKey} type="password" autocomplete="off" placeholder="Leave blank to keep existing secret" />
                </label>
                <div class="provider-meta">
                  <div class="helper-text">{diagnostic?.host || form.baseUrl || 'No host configured'} · {diagnostic?.scheme ?? 'n/a'} · {diagnostic?.live_timeout_seconds ?? 0}s live</div>
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

  .provider-meta {
    display: grid;
    gap: 4px;
    padding: 12px 14px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
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
