<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import { onMount } from 'svelte';

  import { fetchCacheStatus } from '#lib/api/client.ts';
  import JsonView from '#lib/components/JsonView.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { CacheStatusPayload } from '#lib/types.ts';

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
  <PageHeader
    eyebrow="Operate"
    title="Cache"
    description="Review process-local and Redis-backed cache behavior, TTL boundaries, and best-effort invalidation fan-out."
  >
    <div slot="actions">
      <Button class="btn-secondary btn-sm" type="button" onclick={loadCache}>Refresh</Button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect cache status.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading cache status…</div>
  {:else if cacheStatus}
    <div class="grid-3">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <span class="sidebar-label">L1 cache</span>
            <h2 class="section-title">Process-local</h2>
          </div>
          <StatusBadge
            tone={cacheStatus.l1.enabled ? 'success' : 'muted'}
            label={cacheStatus.l1.enabled ? 'Enabled' : 'Disabled'}
          />
        </div>
        <JsonView value={cacheStatus.l1} />
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <span class="sidebar-label">L2 cache</span>
            <h2 class="section-title">Distributed store</h2>
          </div>
          <StatusBadge
            tone={cacheStatus.l2.enabled ? 'success' : 'warning'}
            label={cacheStatus.l2.backend}
          />
        </div>
        <JsonView value={cacheStatus.l2} />
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <span class="sidebar-label">Invalidation</span>
            <h2 class="section-title">Redis fan-out</h2>
          </div>
          <StatusBadge
            tone={cacheStatus.pubsub.enabled ? 'brand' : 'muted'}
            label={cacheStatus.pubsub.enabled ? 'Pub/Sub active' : 'No fan-out'}
          />
        </div>
        <JsonView value={cacheStatus.pubsub} />
      </section>
    </div>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Operator notes</h2>
            <p class="panel-meta">Consistency expectations for this cache layer.</p>
          </div>
        </div>
        <div class="stack-list">
          {#each cacheStatus.notes as note}
            <div class="notice-card">{note}</div>
          {/each}
          {#if cacheStatus.notes.length === 0}
            <div class="empty-state">No operator notes returned.</div>
          {/if}
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Cache health</h2>
            <p class="panel-meta">Observed dependency state.</p>
          </div>
          <StatusBadge
            tone={cacheStatus.health?.status === 'ok' ? 'success' : 'warning'}
            label={cacheStatus.health?.status ?? 'unknown'}
          />
        </div>
        <JsonView value={cacheStatus.health?.components ?? {}} />
      </section>
    </div>
  {/if}
</section>
