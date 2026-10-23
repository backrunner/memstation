<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import { fetchQueues, fetchTasks } from '#lib/api/client.ts';
  import JsonView from '#lib/components/JsonView.svelte';
  import KpiCard from '#lib/components/KpiCard.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { QueueInfo, QueueTaskRecord } from '#lib/types.ts';

  let loading = true;
  let error = '';
  let queue: QueueInfo | null = null;
  let tasks: QueueTaskRecord[] = [];
  let selectedTask: QueueTaskRecord | null = null;
  let selectedStates = ['retry', 'archived', 'scheduled', 'active', 'pending'];
  let lastRouteKey = '';

  onMount(async () => {
    await loadQueue();
  });

  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${$page.params.name}:${selectedStates.join(',')}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastRouteKey && !$targetState.session.checking) {
    lastRouteKey = routeKey;
    void loadQueue();
  }

  async function loadQueue(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !$page.params.name) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      const [queues, taskItems] = await Promise.all([
        fetchQueues($selectedTarget),
        fetchTasks($selectedTarget, {
          queue: $page.params.name,
          states: selectedStates,
          limit: 30
        })
      ]);
      queue = queues.find((item) => item.name === $page.params.name) ?? null;
      tasks = taskItems;
      selectedTask = taskItems[0] ?? null;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load queue detail';
    } finally {
      loading = false;
    }
  }

  function toggleState(state: string): void {
    if (selectedStates.includes(state)) {
      selectedStates = selectedStates.filter((item) => item !== state);
    } else {
      selectedStates = [...selectedStates, state];
    }
  }

  function queueTone(item: QueueInfo | null): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (!item) {
      return 'muted';
    }
    if ((item.retry ?? 0) > 0 || (item.pending ?? 0) > 10) {
      return 'warning';
    }
    if (item.paused) {
      return 'danger';
    }
    return 'success';
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Operations / Queue"
    title={$page.params.name}
    description="Inspect this worker lane, review backlog posture, and jump into the tasks currently occupying it."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect queue detail.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading queue detail...</div>
  {:else if !queue}
    <div class="error-state">Queue `{$page.params.name}` is not available on the selected target.</div>
  {:else}
    <div class="inline-group" style="justify-content: space-between; flex-wrap: wrap;">
      <div class="inline-group" style="flex-wrap: wrap;">
        <a class="btn-ghost" href="/operations/queues">Back to queues</a>
        <a class="btn-ghost" href={`/operations/tasks?queue=${encodeURIComponent(queue.name)}`}>Open task board</a>
      </div>
      <div class="inline-group" style="flex-wrap: wrap;">
        <StatusBadge tone={queueTone(queue)} label={queue.paused ? 'paused' : 'active'} />
        <Button class="btn-secondary" type="button" onclick={loadQueue}>Refresh queue</Button>
      </div>
    </div>

    <div class="kpi-grid">
      <KpiCard label="Pending" value={String(queue.pending)} meta="Waiting to be processed." />
      <KpiCard label="Active" value={String(queue.active)} meta="Currently executing." />
      <KpiCard label="Retry" value={String(queue.retry)} meta="Tasks waiting for retry." />
      <KpiCard label="Latency" value={`${queue.latency_ms ?? 0} ms`} meta="Observed queue latency." />
    </div>

    <section class="card">
      <div class="inline-group" style="justify-content: space-between; flex-wrap: wrap;">
        <div class="inline-group" style="flex-wrap: wrap;">
          {#each ['retry', 'archived', 'scheduled', 'active', 'pending'] as state}
            <Button class={selectedStates.includes(state) ? 'button-secondary' : 'button-ghost'} type="button" onclick={() => toggleState(state)}>{state}</Button>
          {/each}
        </div>
        <div class="helper-text">Showing {tasks.length} tasks from queue `{queue.name}`.</div>
      </div>
    </section>

    <div class="grid-2" style="align-items: start;">
      <section class="table-card">
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>Task</th>
                <th>State</th>
                <th>Conversation</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody>
              {#each tasks as item}
                <tr class="is-selectable" class:is-active={selectedTask?.id === item.id} on:click={() => (selectedTask = item)}>
                  <td>
                    <Button class="table-select-button" type="button" aria-pressed={selectedTask?.id === item.id} onclick={() => (selectedTask = item)}>{item.type}</Button>
                    <div class="subtle-text">{item.id}</div>
                    <div style="margin-top: 8px;">
                      <a class="btn-ghost" href={`/operations/tasks/${encodeURIComponent(item.queue)}/${encodeURIComponent(item.id)}`}>Open detail</a>
                    </div>
                  </td>
                  <td>
                    <StatusBadge tone={item.state === 'active' ? 'brand' : item.state === 'retry' || item.state === 'archived' ? 'warning' : 'muted'} label={item.state} />
                  </td>
                  <td>{item.conversation_id ?? 'n/a'}</td>
                  <td>{item.last_error || 'No error captured'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="debug-column">
        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Queue Metrics</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Current lane posture</h2>
            </div>
          </div>
          <JsonView value={queue} />
        </div>

        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Selected Task</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Payload preview</h2>
            </div>
          </div>
          {#if selectedTask}
            <div class="toolbar">
              <div class="inline-group"><strong>Type</strong><span>{selectedTask.type}</span></div>
              <div class="inline-group"><strong>Retries</strong><span>{selectedTask.retried ?? 0} / {selectedTask.max_retry ?? 0}</span></div>
              <JsonView value={selectedTask.payload ?? {}} emptyLabel="No payload available" />
            </div>
          {:else}
            <div class="empty-state">Select a task row to inspect it.</div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
</section>
