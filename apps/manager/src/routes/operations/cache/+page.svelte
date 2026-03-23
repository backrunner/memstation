<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchCacheStatus } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { CacheStatusPayload } from '$lib/types';

  let loading = true;
  let error = '';
  let lastLoadKey = '';
  let cacheStatus: CacheStatusPayload | null = null;

  onMount(async () => {
    await loadCache();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadCache();
  }

  async function loadCache(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      cacheStatus = await fetchCacheStatus($selectedTarget);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load cache status';
    } finally {
      loading = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Operations / Cache</span>
    <h1 class="page-title">L1/L2 cache posture and invalidation topology</h1>
    <p class="page-description">
      Review how process-local and Redis-backed caches behave on the current target, including TTL boundaries and best-effort invalidation fan-out.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect cache status.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading cache status...</div>
  {:else if cacheStatus}
    <div class="grid-3">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">L1 cache</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Process-local</h2>
          </div>
          <StatusBadge tone={cacheStatus.l1.enabled ? 'success' : 'muted'} label={cacheStatus.l1.enabled ? 'Enabled' : 'Disabled'} />
        </div>
        <JsonView value={cacheStatus.l1} />
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">L2 cache</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Distributed store</h2>
          </div>
          <StatusBadge tone={cacheStatus.l2.enabled ? 'success' : 'warning'} label={cacheStatus.l2.backend} />
        </div>
        <JsonView value={cacheStatus.l2} />
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Invalidation</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Redis fan-out</h2>
          </div>
          <StatusBadge tone={cacheStatus.pubsub.enabled ? 'brand' : 'muted'} label={cacheStatus.pubsub.enabled ? 'Pub/Sub active' : 'No fan-out'} />
        </div>
        <JsonView value={cacheStatus.pubsub} />
      </section>
    </div>

    <div class="grid-2">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Operator notes</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Consistency expectations</h2>
          </div>
        </div>
        <div class="toolbar">
          {#each cacheStatus.notes as note}
            <div class="notice-card">{note}</div>
          {/each}
        </div>
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Cache health</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Observed dependency state</h2>
          </div>
          <StatusBadge tone={cacheStatus.health?.status === 'ok' ? 'success' : 'warning'} label={cacheStatus.health?.status ?? 'unknown'} />
        </div>
        <JsonView value={cacheStatus.health?.components ?? {}} />
      </section>
    </div>
  {/if}
</section>
