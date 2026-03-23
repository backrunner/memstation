<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchQueues } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { QueueInfo } from '$lib/types';

  let loading = true;
  let error = '';
  let queues: QueueInfo[] = [];
  let lastLoadKey = '';

  onMount(async () => {
    await loadQueues();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadQueues();
  }

  async function loadQueues(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      queues = await fetchQueues($selectedTarget);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load queue data';
    } finally {
      loading = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Operations / Queues</span>
    <h1 class="page-title">Queue backlog and worker lane visibility</h1>
    <p class="page-description">
      Watch pending, active, scheduled, and retry counts by queue so you can spot buildup before it impacts summary, analysis, or embedding latency.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect queue state.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading queue metrics...</div>
  {:else}
    <section class="table-card">
      <div class="table-scroll">
        <table class="data-table">
          <thead>
            <tr>
              <th>Queue</th>
              <th>Pending</th>
              <th>Active</th>
              <th>Scheduled</th>
              <th>Retry</th>
              <th>Archived</th>
              <th>Latency</th>
            </tr>
          </thead>
          <tbody>
            {#each queues as queue}
              <tr>
                <td>
                  <div class="inline-group" style="justify-content: space-between; flex-wrap: wrap;">
                    <div class="inline-group">
                      <strong>{queue.name}</strong>
                      <StatusBadge tone={queue.pending > 10 ? 'warning' : 'success'} label={queue.pending > 10 ? 'Backlog' : 'Healthy'} />
                    </div>
                    <div class="inline-group" style="flex-wrap: wrap;">
                      <a class="button-ghost" href={`/operations/queues/${encodeURIComponent(queue.name)}`}>Open queue</a>
                      <a class="button-ghost" href={`/operations/tasks?queue=${encodeURIComponent(queue.name)}`}>Open tasks</a>
                    </div>
                  </div>
                </td>
                <td>{queue.pending}</td>
                <td>{queue.active}</td>
                <td>{queue.scheduled}</td>
                <td>{queue.retry}</td>
                <td>{queue.archived ?? 0}</td>
                <td>{queue.latency_ms ?? 0} ms</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
</section>
