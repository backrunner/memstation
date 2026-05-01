<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchQueues } from '$lib/api/client';
  import PageHeader from '$lib/components/PageHeader.svelte';
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
  <PageHeader
    eyebrow="Operate"
    title="Queues"
    description="Watch pending, active, scheduled, and retry counts by queue to spot buildup before it impacts summary, analysis, or embedding latency."
  >
    <div slot="actions">
      <button class="btn-secondary btn-sm" type="button" on:click={loadQueues}>Refresh</button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect queue state.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading queue metrics…</div>
  {:else}
    <section class="table-card">
      <div class="table-scroll">
        <table class="data-table">
          <thead>
            <tr>
              <th>Queue</th>
              <th>Status</th>
              <th>Pending</th>
              <th>Active</th>
              <th>Scheduled</th>
              <th>Retry</th>
              <th>Archived</th>
              <th>Latency</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each queues as queue}
              <tr>
                <td>
                  <strong class="mono">{queue.name}</strong>
                </td>
                <td>
                  <StatusBadge
                    withDot
                    tone={queue.pending > 10 ? 'warning' : 'success'}
                    label={queue.pending > 10 ? 'Backlog' : 'Healthy'}
                  />
                </td>
                <td class="mono">{queue.pending}</td>
                <td class="mono">{queue.active}</td>
                <td class="mono">{queue.scheduled}</td>
                <td class="mono">{queue.retry}</td>
                <td class="mono">{queue.archived ?? 0}</td>
                <td class="mono">{queue.latency_ms ?? 0} ms</td>
                <td class="text-right">
                  <div class="inline-group" style="justify-content: flex-end;">
                    <a class="btn-ghost btn-sm" href={`/operations/queues/${encodeURIComponent(queue.name)}`}>Open</a>
                    <a class="btn-ghost btn-sm" href={`/operations/tasks?queue=${encodeURIComponent(queue.name)}`}>Tasks</a>
                  </div>
                </td>
              </tr>
            {/each}
            {#if queues.length === 0}
              <tr>
                <td colspan="9">
                  <div class="empty-state" style="border: none; padding: 12px 0;">No queue metrics returned.</div>
                </td>
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
</section>
