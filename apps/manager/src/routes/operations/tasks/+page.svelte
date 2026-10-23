<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

	import { activateMemoryRebuild, discardMemoryRebuild, fetchMemoryRebuilds, fetchQueues, fetchTasks, requeueTask } from '#lib/api/client.ts';
  import CodeBlock from '#lib/components/CodeBlock.svelte';
  import ConfirmActionDialog from '#lib/components/ConfirmActionDialog.svelte';
  import FilterToolbar from '#lib/components/FilterToolbar.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { MemoryGeneration, QueueInfo, QueueTaskRecord } from '#lib/types.ts';

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
  let lastRouteQueue: string | undefined;
  let mounted = false;
  let requestRevision = 0;
  let filterTimer: ReturnType<typeof setTimeout> | undefined;
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
  let confirmOpen = false;
  let confirmTitle = '';
  let confirmDescription = '';
  let confirmSubject = '';
  let confirmLabel = '';
  let pendingAction: (() => Promise<void>) | null = null;

  $: queueItems = [{ value: '', label: 'All queues' }, ...queues.map((queue) => ({ value: queue.name, label: queue.name }))];

  onMount(() => {
    mounted = true;
    return () => { clearTimeout(filterTimer); requestRevision++; };
  });

  $: routeQueue = $page.url.searchParams.get('queue') ?? '';
  $: if (routeQueue !== lastRouteQueue) {
    lastRouteQueue = routeQueue;
    selectedQueue = routeQueue;
  }

  $: queueFilterKey =
    `${$selectedTarget?.id ?? 'none'}:${selectedQueue}:${selectedStates.join(',')}:` +
    `${rebuildUserId.trim()}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (mounted && queueFilterKey !== lastQueueFilter && !$targetState.session.checking) {
    lastQueueFilter = queueFilterKey;
    requestRevision++;
    clearTimeout(filterTimer);
    filterTimer = setTimeout(() => void loadTasks(), 200);
  }

  async function loadTasks(): Promise<void> {
    clearTimeout(filterTimer);
    const revision = ++requestRevision;
    const scope = queueFilterKey;
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      const [nextQueues, nextTasks, nextRebuilds] = await Promise.all([
        fetchQueues($selectedTarget),
        selectedStates.length
          ? fetchTasks($selectedTarget, { limit: 20, states: selectedStates, queue: selectedQueue })
          : Promise.resolve([]),
        rebuildUserId.trim()
          ? fetchMemoryRebuilds($selectedTarget, rebuildUserId.trim(), 12).catch(() => [])
          : Promise.resolve([])
      ]);
      if (revision !== requestRevision || scope !== queueFilterKey) return;
      queues = nextQueues;
      tasks = nextTasks;
      rebuilds = nextRebuilds;
      selectedTask = tasks.find((task) => task.id === selectedTask?.id && task.queue === selectedTask?.queue) ?? tasks[0] ?? null;
    } catch (cause) {
      if (revision === requestRevision && scope === queueFilterKey) error = cause instanceof Error ? cause.message : 'Failed to load tasks';
    } finally {
      if (revision === requestRevision && scope === queueFilterKey) loading = false;
    }
  }

  function toggleState(state: string): void {
    if (selectedStates.includes(state)) {
      selectedStates = selectedStates.filter((item) => item !== state);
    } else {
      selectedStates = [...selectedStates, state];
    }
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

  async function discardGeneration(generationId: string): Promise<void> {
    if (!$selectedTarget || !rebuildUserId.trim()) return;
    actionLoading = true;
    error = '';
    success = '';
    try {
      await discardMemoryRebuild($selectedTarget, rebuildUserId.trim(), generationId);
      success = 'Generation discarded and its inactive graph data was removed.';
      await loadTasks();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to discard memory generation';
    } finally {
      actionLoading = false;
    }
  }

  function stateTone(state: string): 'warning' | 'brand' | 'muted' | 'success' | 'danger' {
    if (state === 'archived' || state === 'retry') return 'warning';
    if (state === 'active') return 'brand';
    return 'muted';
  }

  function requestConfirmation(
    title: string,
    description: string,
    subject: string,
    label: string,
    action: () => Promise<void>
  ): void {
    confirmTitle = title;
    confirmDescription = description;
    confirmSubject = subject;
    confirmLabel = label;
    pendingAction = action;
    confirmOpen = true;
  }

  async function confirmPendingAction(): Promise<void> {
    const action = pendingAction;
    if (!action) return;
    await action();
    confirmOpen = false;
    pendingAction = null;
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Operate"
    title="Tasks"
    description="Review archived, retry, scheduled, and active work items, then re-run by queue identity or publish a manual follow-up task."
  >
    <div slot="actions">
      <Button class="btn-secondary btn-sm" type="button" onclick={loadTasks}>Refresh</Button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect worker tasks.</div>
  {:else}
    <section class="card page-stack">
      <FilterToolbar activeCount={Number(selectedStates.length !== 4 || selectedStates.includes('pending')) + Number(rebuildUserId !== 'admin')}>
        <SelectField label="Queue" items={queueItems} bind:value={selectedQueue} />
        {#snippet filters()}
          <div class="field">
            <span class="field-label">State</span>
            <div class="segmented-control" style="flex-wrap: wrap;">
              {#each stateFilters as state}
                <Button class="segmented-control-item" type="button" data-state={selectedStates.includes(state) ? 'on' : 'off'} aria-pressed={selectedStates.includes(state)} onclick={() => toggleState(state)}>{state}</Button>
              {/each}
            </div>
          </div>
          <TextInputField label="Rebuild user filter" bind:value={rebuildUserId} placeholder="user id" />
        {/snippet}
      </FilterToolbar>
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
            <table class="data-table task-table">
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
                      <Button class="table-select-button mono" type="button" aria-pressed={selectedTask?.id === task.id} onclick={() => (selectedTask = task)}>{task.type}</Button>
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
                          <Button
                            class="btn-link"
                            type="button"
                            onclick={(event) => { event.stopPropagation(); (() => { selectedQueue = task.queue; })(); }}
                          >Filter</Button>
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
                <h2 class="section-title">Selected task</h2>
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
              {#if selectedTask.last_error}<div class="error-state">{selectedTask.last_error}</div>{/if}
              <div>
                <div class="field-label" style="margin-bottom: 6px;">Payload</div>
                <CodeBlock code={JSON.stringify(selectedTask.payload ?? {}, null, 2)} language="json" />
              </div>
              <div class="inline-group" style="justify-content: flex-end;">
                <Button
                  class="btn-primary btn-sm"
                  type="button"
                  disabled={actionLoading}
                  onclick={() => selectedTask && requestConfirmation(
                    'Run this task again?',
                    'The selected payload will be republished to its worker queue immediately.',
                    `${selectedTask.queue} · ${selectedTask.id}`,
                    'Run task now',
                    runSelectedTask
                  )}
                >
                  {actionLoading ? 'Requeueing…' : 'Run task now'}
                </Button>
              </div>
            {:else}
              <div class="empty-state">Select a task row to inspect its payload.</div>
            {/if}
          </section>

          <CollapsibleSection title="Generation lifecycle" variant="panel" open={rebuilds.length > 0}>
            <div class="page-stack">
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
					{#if rebuild.scope === 'conversation' && rebuild.status !== 'discarded'}
						<div class="helper-text">Conversation-scoped rebuilds are inspection-only candidates.</div>
					{/if}
					{#if !rebuild.is_active && rebuild.status !== 'discarded'}
						<div class="inline-group" style="justify-content: flex-end;">
							{#if rebuild.scope !== 'conversation' && rebuild.status === 'completed'}
							<Button
                          class="btn-secondary btn-sm"
                          type="button"
                          disabled={actionLoading}
                          onclick={() => requestConfirmation(
                            'Activate memory generation?',
                            'Active reads will switch to this completed generation for the selected user.',
                            rebuild.generation_id,
                            'Activate generation',
                            () => activateGeneration(rebuild.generation_id)
                          )}
							>
								{actionLoading ? 'Activating…' : 'Activate generation'}
							</Button>
							{/if}
							<Button
								class="btn-danger btn-sm"
								type="button"
								disabled={actionLoading}
								onclick={() => requestConfirmation(
									'Discard memory generation?',
									'Inactive graph, vector, and source references for this generation will be removed.',
									rebuild.generation_id,
									'Discard generation',
									() => discardGeneration(rebuild.generation_id)
								)}
							>Discard</Button>
						</div>
					{/if}
                  </div>
                {/each}
              </div>
            {/if}
            </div>
          </CollapsibleSection>

          <CollapsibleSection title="Publish manual task" variant="panel">
            <div class="page-stack">
<div class="form-grid two-up">
              <SelectField label="Task type" bind:value={manualTaskType} items={taskTypeItems} />
              <TextInputField label="User ID" bind:value={manualUserId} />
              <TextInputField label="Conversation ID" bind:value={manualConversationId} placeholder="conv-…" fieldClass="field-span-2" />
            </div>
            <div class="inline-group" style="justify-content: flex-end;">
              <Button
                class="btn-secondary btn-sm"
                type="button"
                disabled={actionLoading || !manualConversationId.trim()}
                onclick={() => requestConfirmation(
                  'Publish manual task?',
                  'A new worker task will be queued for this conversation.',
                  `${manualTaskType} · ${manualConversationId.trim()}`,
                  'Publish task',
                  runManualRequeue
                )}
              >Publish task</Button>
            </div>
            </div>
          </CollapsibleSection>
        </div>
      </div>
    {/if}
  {/if}
</section>

<ConfirmActionDialog
  bind:open={confirmOpen}
  title={confirmTitle}
  description={confirmDescription}
  subject={confirmSubject}
  confirmLabel={confirmLabel}
  tone="primary"
  busy={actionLoading}
  on:cancel={() => (pendingAction = null)}
  on:confirm={confirmPendingAction}
/>

<style>
  .meta-grid.compact {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }

  .meta-grid.compact > div {
    padding: 0;
    border: 0;
    background: transparent;
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
    overflow-wrap: anywhere;
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
    min-width: 0;
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

  @media (max-width: 640px) {
    .task-table th:nth-child(n + 3), .task-table td:nth-child(n + 3) { display: none; }
  }
</style>
