<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConfig, fetchHealth, fetchProviderDiagnostics, fetchQueues } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AdminConfigPayload, HealthPayload, ProviderDiagnostic, QueueInfo } from '$lib/types';

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
    const liveValue = config?.runtime_live?.[key as keyof NonNullable<typeof config.runtime_live>];
    return meta.apply_mode === 'live' && JSON.stringify(desired) !== JSON.stringify(liveValue);
  }).length;

  $: backlogTotal = queues.reduce((sum, queue) => sum + queue.pending + queue.retry + (queue.archived ?? 0), 0);
  $: worstLatency = queues.reduce((max, queue) => Math.max(max, queue.latency_ms ?? 0), 0);
  $: degradedProviders = providers.filter((item) => item.status === 'degraded').length;
  $: warningProviders = providers.filter((item) => item.status === 'warning').length;
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Operations / Health</span>
    <h1 class="page-title">Live and readiness signals</h1>
    <p class="page-description">
      Compare liveness and readiness responses, then drill into per-component readiness to identify degraded dependencies quickly.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect target health.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading target health...</div>
  {:else}
    <div class="kpi-grid">
      <div class="kpi-card">
        <span class="kpi-label">Readiness</span>
        <span class="kpi-value">{ready?.status ?? 'unknown'}</span>
        <span class="kpi-meta">Current dependency gate.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Restart Pending</span>
        <span class="kpi-value">{restartPending}</span>
        <span class="kpi-meta">Saved runtime settings not yet active in this process.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Live Overrides</span>
        <span class="kpi-value">{liveOverrides}</span>
        <span class="kpi-meta">Hot-applied runtime changes active on the target.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Queue Pressure</span>
        <span class="kpi-value">{backlogTotal}</span>
        <span class="kpi-meta">Pending, retry, and archived tasks across worker lanes.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Provider Issues</span>
        <span class="kpi-value">{degradedProviders + warningProviders}</span>
        <span class="kpi-meta">Credential or endpoint posture requiring review.</span>
      </div>
    </div>

    <div class="grid-2">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Liveness</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Process heartbeat</h2>
          </div>
          <StatusBadge tone={live?.status === 'live' ? 'success' : 'danger'} label={live?.status ?? 'unknown'} />
        </div>
        <div class="toolbar">
          <div class="debug-block">
            <div class="sidebar-label">Version</div>
            <div>{live?.version ?? 'n/a'}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Mode</div>
            <div>{live?.mode ?? 'n/a'}</div>
          </div>
        </div>
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Readiness</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Dependency gate</h2>
          </div>
          <StatusBadge tone={ready?.status === 'ready' ? 'success' : 'warning'} label={ready?.status ?? 'unknown'} />
        </div>
        {#if !ready?.services}
          <div class="empty-state">This target did not return readiness component details.</div>
        {:else}
          <div class="toolbar">
            {#each Object.entries(ready.services) as [name, status]}
              <div class="debug-block">
                <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                  <strong>{name}</strong>
                  <StatusBadge tone={status === 'ok' ? 'success' : 'warning'} label={status} />
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>

    <div class="grid-2">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Runtime drift</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Desired versus live configuration</h2>
          </div>
          <a class="button-ghost" href="/settings/system">Open system settings</a>
        </div>
        <div class="toolbar">
          {#each Object.entries(config?.runtime_meta ?? {}) as [key, meta]}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                <strong>{key}</strong>
                <div class="runtime-field-status">
                  <StatusBadge tone={meta.apply_mode === 'live' ? 'success' : 'warning'} label={meta.apply_mode} />
                  <StatusBadge tone={meta.source === 'override' ? 'brand' : 'muted'} label={meta.source} />
                </div>
              </div>
              <div class="helper-text">{meta.description}</div>
              <div class="helper-text" style="margin-top: 8px;">desired: {JSON.stringify(config?.runtime?.[key as keyof NonNullable<typeof config.runtime>])}</div>
              <div class="helper-text">live: {JSON.stringify(config?.runtime_live?.[key as keyof NonNullable<typeof config.runtime_live>])}</div>
            </div>
          {/each}
        </div>
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Queue posture</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Worker lane pressure at a glance</h2>
          </div>
          <div class="runtime-field-status">
            <StatusBadge tone={worstLatency > 1000 ? 'warning' : 'success'} label={`${worstLatency} ms worst latency`} />
            <a class="button-ghost" href="/operations/queues">Open queues</a>
          </div>
        </div>
        <div class="toolbar">
          {#each queues as queue}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                <strong>{queue.name}</strong>
                <StatusBadge tone={queue.pending + queue.retry > 10 ? 'warning' : 'success'} label={queue.pending + queue.retry > 10 ? 'watch' : 'ok'} />
              </div>
              <div class="helper-text">pending {queue.pending} · active {queue.active} · retry {queue.retry}</div>
              <div class="helper-text">archived {queue.archived ?? 0} · latency {queue.latency_ms ?? 0} ms</div>
            </div>
          {/each}
        </div>
      </section>
    </div>

    <section class="card">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Provider posture</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Credential, endpoint, and drift diagnostics</h2>
        </div>
        <a class="button-ghost" href="/settings/providers">Open provider settings</a>
      </div>
      <div class="toolbar">
        {#each providers as provider}
          <div class="debug-block">
            <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
              <strong>{provider.scope} / {provider.provider}</strong>
              <div class="runtime-field-status">
                <StatusBadge tone={provider.status === 'ready' ? 'success' : provider.status === 'degraded' ? 'danger' : 'warning'} label={provider.status} />
                {#if provider.is_desired_default}
                  <StatusBadge tone="brand" label="desired default" />
                {/if}
              </div>
            </div>
            <div class="helper-text">{provider.host ?? provider.base_url}</div>
            <div class="helper-text">timeout {provider.live_timeout_seconds}s live / {provider.desired_timeout_seconds}s desired</div>
            {#if (provider.issues.length ?? 0) > 0}
              <div class="helper-text" style="margin-top: 8px;">{provider.issues[0]}</div>
            {/if}
          </div>
        {/each}
      </div>
    </section>
  {/if}
</section>
