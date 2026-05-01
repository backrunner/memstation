<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { activateMemoryRebuild, fetchMemoryRebuilds, fetchQueues, fetchTasks, requeueTask } from '$lib/api/client';
  import CodeBlock from '$lib/components/CodeBlock.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import SelectField from '$lib/components/SelectField.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { MemoryGeneration, QueueInfo, QueueTaskRecord } from '$lib/types';

  const taskTypeItems = [
    { value: 'analysis.extract', label: 'analysis.extract' },
    { value: 'summary.generate', label: 'summary.generate' },
    { value: 'keymemory.reconcile', label: 'keymemory.reconcile' },
    { value: 'conversation.finalize', label: 'conversation.finalize' }
  ];

  const stateFilters = ['retry', 'archived', 'scheduled', 'active', 'pending'];

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
  let queueItems: Array<{ value: string; label: string }> = [{ value: '', label: 'All queues' }];

  $: queueItems = [{ value: '', label: 'All queues' }, ...queues.map((queue) => ({ value: queue.name, label: queue.name }))];

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
        rebuildUserId.trim()
          ? fetchMemoryRebuilds($selectedTarget, rebuildUserId.trim(), 12).catch(() => [])
          : Promise.resolve([])
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

  function stateTone(state: string): 'warning' | 'brand' | 'muted' | 'success' | 'danger' {
    if (state === 'archived' || state === 'retry') return 'warning';
    if (state === 'active') return 'brand';
    return 'muted';
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Operate"
    title="Tasks"
    description="Review archived, retry, scheduled, and active work items, then re-run by queue identity or publish a manual follow-up task."
  >
    <div slot="actions">
      <button class="btn-secondary btn-sm" type="button" on:click={loadTasks}>Refresh</button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect worker tasks.</div>
  {:else}
    <section class="card page-stack">
      <div class="page-header">
        <div class="page-header-meta">
          <h2 class="section-title">Filters</h2>
          <p class="panel-meta">Narrow by state, queue, and rebuild user scope.</p>
        </div>
      </div>
      <div class="filter-bar">
        <div class="filter-bar-states">
          <span class="field-label">State</span>
          <div class="segmented-control" style="flex-wrap: wrap;">
            {#each stateFilters as state}
              <button
                class="segmented-control-item"
                type="button"
                data-state={selectedStates.includes(state) ? 'on' : 'off'}
                on:click={() => toggleState(state)}
              >{state}</button>
            {/each}
          </div>
        </div>
        <div class="filter-bar-selects">
          <SelectField label="Queue" items={queueItems} bind:value={selectedQueue} fieldClass="filter-bar-field" />
          <TextInputField label="Rebuild user filter" bind:value={rebuildUserId} placeholder="user id" fieldClass="filter-bar-field" />
        </div>
      </div>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {:else if loading}
      <div class="notice-card">Loading task data…</div>
    {:else}
      {#if success}
        <div class="notice-card is-success">{success}</div>
      {/if}

      <div class="page-columns">
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
                  <tr
                    class="is-selectable"
                    class:is-active={selectedTask?.id === task.id}
                    on:click={() => (selectedTask = task)}
                  >
                    <td>
                      <strong class="mono">{task.type}</strong>
                      <div class="subtle-text mono">{task.id}</div>
                      <a class="btn-link" style="margin-top: 4px;" href={`/operations/tasks/${encodeURIComponent(task.queue)}/${encodeURIComponent(task.id)}`}>
                        Open detail
                      </a>
                    </td>
                    <td>
                      <StatusBadge tone={stateTone(task.state)} label={task.state} />
                    </td>
                    <td class="mono">
                      {task.queue}
                      {#if selectedQueue !== task.queue}
                        <div style="margin-top: 4px;">
                          <button
                            class="btn-link"
                            type="button"
                            on:click|stopPropagation={() => { selectedQueue = task.queue; void loadTasks(); }}
                          >Filter</button>
                        </div>
                      {/if}
                    </td>
                    <td class="subtle-text mono">{task.conversation_id ?? 'n/a'}</td>
                    <td class="subtle-text">{task.last_error || 'No error captured'}</td>
                  </tr>
                {/each}
                {#if tasks.length === 0}
                  <tr>
                    <td colspan="5">
                      <div class="empty-state" style="border: none; padding: 12px 0;">No tasks match the current filter.</div>
                    </td>
                  </tr>
                {/if}
              </tbody>
            </table>
          </div>
        </section>

        <div class="page-column">
          <section class="card page-stack">
            <div class="page-header">
              <div class="page-header-meta">
                <h2 class="section-title">Payload &amp; retry context</h2>
                <p class="panel-meta">Inspect the selected task before requeuing.</p>
              </div>
            </div>
            {#if selectedTask}
              <dl class="meta-grid compact">
                <div>
                  <dt>Type</dt>
                  <dd class="mono">{selectedTask.type}</dd>
                </div>
                <div>
                  <dt>Queue</dt>
                  <dd class="mono">{selectedTask.queue}</dd>
                </div>
                <div>
                  <dt>Retries</dt>
                  <dd class="mono">{selectedTask.retried ?? 0} / {selectedTask.max_retry ?? 0}</dd>
                </div>
              </dl>
              <div>
                <div class="field-label" style="margin-bottom: 6px;">Payload</div>
                <CodeBlock code={JSON.stringify(selectedTask.payload ?? {}, null, 2)} language="json" />
              </div>
              <div class="inline-group" style="justify-content: flex-end;">
                <button class="btn-primary btn-sm" type="button" disabled={actionLoading} on:click={runSelectedTask}>
                  {actionLoading ? 'Requeueing…' : 'Run task now'}
                </button>
              </div>
            {:else}
              <div class="empty-state">Select a task row to inspect its payload.</div>
            {/if}
          </section>

          <section class="card page-stack">
            <div class="page-header">
              <div class="page-header-meta">
                <h2 class="section-title">Generation lifecycle</h2>
                <p class="panel-meta">Memory rebuild candidates for the filtered user.</p>
              </div>
            </div>
            {#if rebuilds.length === 0}
              <div class="empty-state">No rebuild generations found for this user filter.</div>
            {:else}
              <div class="stack-list">
                {#each rebuilds as rebuild}
                  <div class="rebuild-card">
                    <div class="rebuild-head">
                      <div>
                        <strong>{rebuild.scope}</strong>
                        <div class="subtle-text mono">{rebuild.generation_id}</div>
                      </div>
                      <StatusBadge
                        tone={rebuild.is_active ? 'success' : rebuild.status === 'failed' ? 'danger' : rebuild.status === 'queued' || rebuild.status === 'running' ? 'warning' : 'muted'}
                        label={rebuild.is_active ? 'active' : rebuild.status}
                      />
                    </div>
                    <div class="helper-text">{rebuild.processed_count}/{rebuild.source_count} snapshots · {rebuild.embedding_count} embeddings</div>
                    {#if rebuild.last_error}
                      <div class="helper-text" style="color: var(--danger);">{rebuild.last_error}</div>
                    {/if}
                    {#if rebuild.scope === 'conversation'}
                      <div class="helper-text">Conversation-scoped rebuilds are inspection-only candidates.</div>
                    {:else if !rebuild.is_active && (rebuild.status === 'completed' || rebuild.status === 'activated')}
                      <div class="inline-group" style="justify-content: flex-end;">
                        <button class="btn-secondary btn-sm" type="button" disabled={actionLoading} on:click={() => activateGeneration(rebuild.generation_id)}>
                          {actionLoading ? 'Activating…' : 'Activate generation'}
                        </button>
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </section>

          <section class="card page-stack">
            <div class="page-header">
              <div class="page-header-meta">
                <h2 class="section-title">Publish manual task</h2>
                <p class="panel-meta">Enqueue a follow-up task by conversation id.</p>
              </div>
            </div>
            <div class="form-grid two-up">
              <SelectField label="Task type" bind:value={manualTaskType} items={taskTypeItems} />
              <TextInputField label="User ID" bind:value={manualUserId} />
              <TextInputField label="Conversation ID" bind:value={manualConversationId} placeholder="conv-…" fieldClass="field-span-2" />
            </div>
            <div class="inline-group" style="justify-content: flex-end;">
              <button class="btn-secondary btn-sm" type="button" disabled={actionLoading} on:click={runManualRequeue}>Publish task</button>
            </div>
          </section>
        </div>
      </div>
    {/if}
  {/if}
</section>

<style>
  .filter-bar {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 16px;
    align-items: end;
  }

  .filter-bar-states {
    display: grid;
    gap: 6px;
  }

  .filter-bar-selects {
    display: grid;
    grid-template-columns: 220px 220px;
    gap: 12px;
  }

  :global(.filter-bar-field) { min-width: 0; }

  .meta-grid.compact {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }

  .meta-grid.compact > div {
    padding: 8px 10px;
  }

  .meta-grid dt {
    color: var(--text-muted);
    font-size: var(--font-size-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin: 0;
  }

  .meta-grid dd {
    margin: 0;
    color: var(--text);
    font-size: var(--font-size-md);
    font-weight: 500;
  }

  .meta-grid {
    display: grid;
    gap: 12px;
    margin: 0;
  }

  .meta-grid > div {
    display: grid;
    gap: 3px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
  }

  .rebuild-card {
    display: grid;
    gap: 6px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
  }

  .rebuild-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 8px;
  }

  @media (max-width: 960px) {
    .filter-bar,
    .filter-bar-selects {
      grid-template-columns: 1fr;
    }
  }
</style>
