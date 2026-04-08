<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { Select } from 'bits-ui';

  import { activateMemoryRebuild, fetchMemoryRebuilds, fetchQueues, fetchTasks, requeueTask } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { MemoryGeneration, QueueInfo, QueueTaskRecord } from '$lib/types';

  const taskTypeItems = [
    { value: 'analysis.extract', label: 'analysis.extract' },
    { value: 'summary.generate', label: 'summary.generate' },
    { value: 'keymemory.reconcile', label: 'keymemory.reconcile' },
    { value: 'conversation.finalize', label: 'conversation.finalize' }
  ];

  let loading = true;
  let actionLoading = false;
  let error = '';
  let success = '';
  let lastQueueFilter = '';
  let queues: QueueInfo[] = [];
  let tasks: QueueTaskRecord[] = [];
  let selectedStates = ['retry', 'archived', 'scheduled', 'active'];
  let selectedQueue = '';
  let selectedTask: QueueTaskRecord | null = null;
  let rebuilds: MemoryGeneration[] = [];
  let manualConversationId = '';
  let manualUserId = 'admin';
  let manualTaskType = 'analysis.extract';
  let rebuildUserId = 'admin';

  onMount(async () => {
    await loadTasks();
  });

  $: routeQueue = $page.url.searchParams.get('queue') ?? '';
  $: if (routeQueue !== selectedQueue) {
    selectedQueue = routeQueue;
  }

  $: queueFilterKey =
    `${$selectedTarget?.id ?? 'none'}:${selectedQueue}:${selectedStates.join(',')}:` +
    `${rebuildUserId.trim()}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (queueFilterKey !== lastQueueFilter && !$targetState.session.checking) {
    lastQueueFilter = queueFilterKey;
    void loadTasks();
  }

  async function loadTasks(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      [queues, tasks, rebuilds] = await Promise.all([
        fetchQueues($selectedTarget),
        fetchTasks($selectedTarget, { limit: 20, states: selectedStates, queue: selectedQueue }),
        rebuildUserId.trim() ? fetchMemoryRebuilds($selectedTarget, rebuildUserId.trim(), 12) : Promise.resolve([])
      ]);
      selectedTask = tasks[0] ?? null;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load tasks';
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
    void loadTasks();
  }

  async function runSelectedTask(): Promise<void> {
    if (!$selectedTarget || !selectedTask) {
      return;
    }
    actionLoading = true;
    error = '';
    success = '';
    try {
      await requeueTask($selectedTarget, {
        queue: selectedTask.queue,
        task_id: selectedTask.id
      });
      success = 'Selected task has been requeued immediately.';
      await loadTasks();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to requeue task';
    } finally {
      actionLoading = false;
    }
  }

  async function runManualRequeue(): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    actionLoading = true;
    error = '';
    success = '';
    try {
      await requeueTask($selectedTarget, {
        task_type: manualTaskType,
        conversation_id: manualConversationId.trim(),
        user_id: manualUserId.trim()
      });
      success = 'Manual requeue request accepted by the selected target.';
      await loadTasks();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to requeue task';
    } finally {
      actionLoading = false;
    }
  }

  async function activateGeneration(generationId: string): Promise<void> {
    if (!$selectedTarget || !rebuildUserId.trim()) {
      return;
    }
    actionLoading = true;
    error = '';
    success = '';
    try {
      await activateMemoryRebuild($selectedTarget, rebuildUserId.trim(), generationId);
      success = 'Generation activated. Active reads will switch on the selected target.';
      await loadTasks();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to activate memory generation';
    } finally {
      actionLoading = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Operations / Tasks</span>
    <h1 class="page-title">Inspect failed or delayed tasks and trigger safe requeues</h1>
    <p class="page-description">
      Review archived, retry, scheduled, and active work items, then re-run a task by queue identity or publish a manual follow-up task by conversation id.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect worker tasks.</div>
  {:else}
    <section class="card">
      <div class="inline-group" style="flex-wrap: wrap; justify-content: space-between;">
        <div class="inline-group" style="flex-wrap: wrap; flex: 1;">
          {#each ['retry', 'archived', 'scheduled', 'active', 'pending'] as state}
            <button class={selectedStates.includes(state) ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => toggleState(state)}>{state}</button>
          {/each}
        </div>
        <div class="inline-group" style="flex-wrap: wrap;">
          <label class="field" style="min-width: 220px;">
            <span class="field-label">Queue filter</span>
            <select class="input" bind:value={selectedQueue} on:change={() => void loadTasks()}>
              <option value="">All queues</option>
              {#each queues as queue}
                <option value={queue.name}>{queue.name}</option>
              {/each}
            </select>
          </label>
          <button class="button-secondary" type="button" on:click={loadTasks}>Refresh tasks</button>
        </div>
      </div>
      <div class="inline-group" style="margin-top: 12px;">
        <label class="field" style="min-width: 260px;">
          <span class="field-label">Rebuild user filter</span>
          <input class="input" bind:value={rebuildUserId} placeholder="user id for generation status" />
        </label>
      </div>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {:else if loading}
      <div class="notice-card">Loading task data...</div>
    {:else}
      {#if success}
        <div class="notice-card">{success}</div>
      {/if}
      <div class="grid-2" style="align-items: start;">
        <section class="table-card">
          <div class="table-scroll">
            <table class="data-table">
              <thead>
                <tr>
                  <th>Task</th>
                  <th>State</th>
                  <th>Queue</th>
                  <th>Conversation</th>
                  <th>Error</th>
                </tr>
              </thead>
              <tbody>
                {#each tasks as task}
                  <tr on:click={() => (selectedTask = task)} style="cursor: pointer;">
                    <td>
                      <strong>{task.type}</strong>
                      <div class="subtle-text">{task.id}</div>
                      <div style="margin-top: 8px;">
                        <a class="button-ghost" href={`/operations/tasks/${encodeURIComponent(task.queue)}/${encodeURIComponent(task.id)}`}>Open detail</a>
                      </div>
                    </td>
                    <td>
                      <StatusBadge tone={task.state === 'archived' || task.state === 'retry' ? 'warning' : task.state === 'active' ? 'brand' : 'muted'} label={task.state} />
                    </td>
                    <td>
                      <div class="inline-group" style="flex-wrap: wrap;">
                        <span>{task.queue}</span>
                        {#if selectedQueue !== task.queue}
                          <button class="button-ghost" type="button" on:click|stopPropagation={() => { selectedQueue = task.queue; void loadTasks(); }}>Only this queue</button>
                        {/if}
                      </div>
                    </td>
                    <td>{task.conversation_id ?? 'n/a'}</td>
                    <td>{task.last_error || 'No error captured'}</td>
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
                <div class="sidebar-label">Memory rebuilds</div>
                <h2 class="page-title" style="font-size: 1.2rem;">Generation lifecycle</h2>
              </div>
            </div>
            {#if rebuilds.length === 0}
              <div class="empty-state">No rebuild generations found for the selected user filter.</div>
            {:else}
              <div class="toolbar">
                {#each rebuilds as rebuild}
                  <div class="debug-block" style="background: var(--bg-panel-muted);">
                    <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                      <div>
                        <strong>{rebuild.scope}</strong>
                        <div class="subtle-text">{rebuild.generation_id}</div>
                      </div>
                      <StatusBadge
                        tone={rebuild.is_active ? 'success' : rebuild.status === 'failed' ? 'danger' : rebuild.status === 'queued' || rebuild.status === 'running' ? 'warning' : 'muted'}
                        label={rebuild.is_active ? 'active' : rebuild.status}
                      />
                    </div>
                    <div class="helper-text">{rebuild.processed_count}/{rebuild.source_count} snapshots · {rebuild.embedding_count} embeddings</div>
                    {#if rebuild.last_error}
                      <div class="subtle-text" style="margin-top: 8px;">{rebuild.last_error}</div>
                    {/if}
                    {#if rebuild.scope === 'conversation'}
                      <div class="helper-text" style="margin-top: 10px;">Conversation-scoped rebuilds are inspection-only candidates and cannot replace the user's active generation.</div>
                    {:else if !rebuild.is_active && (rebuild.status === 'completed' || rebuild.status === 'activated')}
                      <div style="margin-top: 10px;">
                        <button class="button-ghost" type="button" disabled={actionLoading} on:click={() => activateGeneration(rebuild.generation_id)}>
                          {actionLoading ? 'Activating...' : 'Activate / rollback to this generation'}
                        </button>
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </div>

          <div class="debug-block">
            <div class="page-header">
              <div>
                <div class="sidebar-label">Selected task</div>
                <h2 class="page-title" style="font-size: 1.2rem;">Payload and retry context</h2>
              </div>
            </div>
            {#if selectedTask}
              <div class="toolbar">
                <div class="inline-group"><strong>Type</strong><span>{selectedTask.type}</span></div>
                <div class="inline-group"><strong>Queue</strong><span>{selectedTask.queue}</span></div>
                <div class="inline-group"><strong>Retries</strong><span>{selectedTask.retried ?? 0} / {selectedTask.max_retry ?? 0}</span></div>
                <pre class="json-block">{JSON.stringify(selectedTask.payload ?? {}, null, 2)}</pre>
                <button class="button" type="button" disabled={actionLoading} on:click={runSelectedTask}>{actionLoading ? 'Requeueing...' : 'Run selected task now'}</button>
              </div>
            {:else}
              <div class="empty-state">Select a task row to inspect its payload.</div>
            {/if}
          </div>

          <div class="debug-block">
            <div class="page-header">
              <div>
                <div class="sidebar-label">Manual requeue</div>
                <h2 class="page-title" style="font-size: 1.2rem;">Publish a fresh task</h2>
              </div>
            </div>
            <div class="toolbar">
              <label class="field">
                <span class="field-label">Task type</span>
                <Select.Root type="single" bind:value={manualTaskType} items={taskTypeItems}>
                  <Select.Trigger class="select-trigger">{manualTaskType}</Select.Trigger>
                  <Select.Portal>
                    <Select.Content class="select-content" sideOffset={8}>
                      <Select.Viewport>
                        {#each taskTypeItems as item}
                          <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                        {/each}
                      </Select.Viewport>
                    </Select.Content>
                  </Select.Portal>
                </Select.Root>
              </label>
              <label class="field">
                <span class="field-label">Conversation ID</span>
                <input class="input" bind:value={manualConversationId} placeholder="conv-..." />
              </label>
              <label class="field">
                <span class="field-label">User ID</span>
                <input class="input" bind:value={manualUserId} />
              </label>
              <button class="button-secondary" type="button" disabled={actionLoading} on:click={runManualRequeue}>Publish manual task</button>
            </div>
          </div>
        </section>
      </div>
    {/if}
  {/if}
</section>
