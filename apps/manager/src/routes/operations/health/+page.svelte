<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, fetchHealth, fetchProviderDiagnostics, fetchQueues } from '$lib/api/client';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AdminConfigPayload, HealthPayload, ProviderDiagnostic, QueueInfo } from '$lib/types';

  type RuntimeDriftEntry = {
    key: string;
    applyMode: string;
    desired: string;
    live: string;
  };

  let loading = true;
  let error = '';
  let config: AdminConfigPayload | null = null;
  let live: HealthPayload | null = null;
  let ready: HealthPayload | null = null;
  let queues: QueueInfo[] = [];
  let providers: ProviderDiagnostic[] = [];
  let lastLoadKey = '';
  let restartPending = 0;
  let liveOverrides = 0;
  let backlogTotal = 0;
  let worstLatency = 0;
  let degradedProviders = 0;
  let warningProviders = 0;
  let runtimeDriftEntries: RuntimeDriftEntry[] = [];

  onMount(async () => {
    await loadHealth();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadHealth();
  }

  async function loadHealth(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      [live, ready, config, queues, providers] = await Promise.all([
        fetchHealth($selectedTarget, '/health/live'),
        fetchHealth($selectedTarget, '/health/ready'),
        fetchConfig($selectedTarget),
        fetchQueues($selectedTarget),
        fetchProviderDiagnostics($selectedTarget)
      ]);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load health status';
    } finally {
      loading = false;
    }
  }

  $: restartPending = Object.entries(config?.runtime_meta ?? {}).filter(([key, meta]) => {
    const desired = config?.runtime?.[key as keyof NonNullable<typeof config.runtime>];
    const liveValue = config?.runtime_live?.[key as keyof NonNullable<typeof config.runtime_live>];
    return meta.apply_mode === 'restart' && JSON.stringify(desired) !== JSON.stringify(liveValue);
  }).length;

  $: liveOverrides = Object.entries(config?.runtime_meta ?? {}).filter(([key, meta]) => {
    const desired = config?.runtime?.[key as keyof NonNullable<typeof config.runtime>];
    const defaults = config?.runtime_defaults?.[key as keyof NonNullable<typeof config.runtime_defaults>];
    const liveValue = config?.runtime_live?.[key as keyof NonNullable<typeof config.runtime_live>];
    return meta.apply_mode === 'live'
      && JSON.stringify(desired) === JSON.stringify(liveValue)
      && JSON.stringify(desired) !== JSON.stringify(defaults);
  }).length;

  $: backlogTotal = queues.reduce((sum, queue) => sum + queue.pending + queue.retry + (queue.archived ?? 0), 0);
  $: worstLatency = queues.reduce((max, queue) => Math.max(max, queue.latency_ms ?? 0), 0);
  $: degradedProviders = providers.filter((item) => item.status === 'degraded').length;
  $: warningProviders = providers.filter((item) => item.status === 'warning').length;
  $: runtimeDriftEntries = Object.entries(config?.runtime_meta ?? {})
    .filter(([key]) => {
      const desired = config?.runtime?.[key as keyof NonNullable<typeof config.runtime>];
      const liveValue = config?.runtime_live?.[key as keyof NonNullable<typeof config.runtime_live>];
      return JSON.stringify(desired) !== JSON.stringify(liveValue);
    })
    .map(([key, meta]) => ({
      key,
      applyMode: meta.apply_mode,
      desired: JSON.stringify(config?.runtime?.[key as keyof NonNullable<typeof config.runtime>]),
      live: JSON.stringify(config?.runtime_live?.[key as keyof NonNullable<typeof config.runtime_live>])
    }));

  function readyTone(status?: string): 'success' | 'warning' | 'danger' {
    if (status === 'ready') return 'success';
    if (status === 'unknown') return 'warning';
    return 'warning';
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Operate"
    title="Health"
    description="Dependency posture, drift, and queue/provider signals for the selected target."
  >
    <div slot="actions">
      <button class="btn-secondary btn-sm" type="button" on:click={loadHealth}>Refresh</button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect target health.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading target health…</div>
  {:else}
    <div class="kpi-grid">
      <article class="kpi-card">
        <span class="kpi-label">Readiness</span>
        <strong class="kpi-value" style="text-transform: capitalize;">{ready?.status ?? 'unknown'}</strong>
        <span class="kpi-meta">Process: {live?.status ?? 'unknown'}</span>
      </article>
      <article class="kpi-card">
        <span class="kpi-label">Restart pending</span>
        <strong class="kpi-value">{restartPending}</strong>
        <span class="kpi-meta">{liveOverrides} live override{liveOverrides === 1 ? '' : 's'}</span>
      </article>
      <article class="kpi-card">
        <span class="kpi-label">Queue backlog</span>
        <strong class="kpi-value">{backlogTotal}</strong>
        <span class="kpi-meta">Worst latency {worstLatency} ms</span>
      </article>
      <article class="kpi-card">
        <span class="kpi-label">Provider issues</span>
        <strong class="kpi-value">{degradedProviders + warningProviders}</strong>
        <span class="kpi-meta">{degradedProviders} degraded · {warningProviders} warning</span>
      </article>
    </div>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Process heartbeat</h2>
            <p class="panel-meta">/health/live snapshot</p>
          </div>
          <StatusBadge
            withDot
            tone={live?.status === 'live' ? 'success' : 'danger'}
            label={live?.status ?? 'unknown'}
          />
        </div>
        <dl class="meta-grid">
          <div>
            <dt>Version</dt>
            <dd class="mono">{live?.version ?? 'n/a'}</dd>
          </div>
          <div>
            <dt>Mode</dt>
            <dd class="mono">{live?.mode ?? 'n/a'}</dd>
          </div>
        </dl>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Dependency gate</h2>
            <p class="panel-meta">/health/ready components</p>
          </div>
          <StatusBadge
            withDot
            tone={readyTone(ready?.status)}
            label={ready?.status ?? 'unknown'}
          />
        </div>
        {#if !ready?.services}
          <div class="empty-state">This target did not return readiness component details.</div>
        {:else}
          <div class="stack-list">
            {#each Object.entries(ready.services) as [name, status]}
              <div class="dep-row">
                <span class="mono">{name}</span>
                <StatusBadge tone={status === 'ok' ? 'success' : 'warning'} label={String(status)} />
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Runtime drift</h2>
            <p class="panel-meta">Desired vs live configuration values.</p>
          </div>
          <a class="btn-ghost btn-sm" href="/settings/system">Open settings</a>
        </div>
        {#if runtimeDriftEntries.length === 0}
          <div class="notice-card is-success">No runtime drift detected.</div>
        {:else}
          <div class="stack-list">
            {#each runtimeDriftEntries as entry}
              <div class="drift-row">
                <div class="drift-row-head">
                  <strong class="mono">{entry.key}</strong>
                  <StatusBadge
                    tone={entry.applyMode === 'live' ? 'danger' : 'warning'}
                    label={entry.applyMode === 'live' ? 'drift' : 'restart'}
                  />
                </div>
                <div class="helper-text">
                  <span class="mono">desired</span> {entry.desired}
                </div>
                <div class="helper-text">
                  <span class="mono">live</span> {entry.live}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Queue posture</h2>
            <p class="panel-meta">Depth and latency across worker queues.</p>
          </div>
          <a class="btn-ghost btn-sm" href="/operations/queues">Open queues</a>
        </div>
        <div class="stack-list">
          {#each queues as queue}
            <div class="queue-row">
              <div class="queue-row-head">
                <strong class="mono">{queue.name}</strong>
                <StatusBadge
                  tone={queue.pending + queue.retry > 10 ? 'warning' : 'success'}
                  label={queue.pending + queue.retry > 10 ? 'watch' : 'ok'}
                />
              </div>
              <div class="helper-text">
                pending {queue.pending} · active {queue.active} · retry {queue.retry} · archived {queue.archived ?? 0}
              </div>
              <div class="helper-text">latency {queue.latency_ms ?? 0} ms</div>
            </div>
          {/each}
        </div>
      </section>
    </div>

    <section class="card page-stack">
      <div class="page-header">
        <div class="page-header-meta">
          <h2 class="section-title">Provider posture</h2>
          <p class="panel-meta">Upstream provider health and configuration.</p>
        </div>
        <a class="btn-ghost btn-sm" href="/settings/providers">Open providers</a>
      </div>
      <div class="grid-2">
        {#each providers as provider}
          <div class="provider-row">
            <div class="provider-row-head">
              <strong>{provider.scope} / {provider.provider}</strong>
              <StatusBadge
                tone={provider.status === 'ready' ? 'success' : provider.status === 'degraded' ? 'danger' : 'warning'}
                label={provider.status}
              />
            </div>
            <div class="helper-text mono">{provider.host ?? provider.base_url}</div>
            <div class="helper-text">timeout {provider.live_timeout_seconds}s live / {provider.desired_timeout_seconds}s desired</div>
            {#if (provider.issues?.length ?? 0) > 0}
              <div class="helper-text" style="color: var(--danger); margin-top: 4px;">{provider.issues[0]}</div>
            {/if}
          </div>
        {/each}
      </div>
    </section>
  {/if}
</section>

<style>
  .meta-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
    margin: 0;
  }

  .meta-grid > div {
    display: grid;
    gap: 4px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
  }

  .meta-grid dt {
    color: var(--text-muted);
    font-size: var(--font-size-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0;
    margin: 0;
  }

  .meta-grid dd {
    margin: 0;
    color: var(--text);
    font-size: var(--font-size-md);
  }

  .dep-row,
  .drift-row,
  .queue-row,
  .provider-row {
    display: grid;
    gap: 4px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
  }

  .dep-row {
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
  }

  .drift-row-head,
  .queue-row-head,
  .provider-row-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
</style>
