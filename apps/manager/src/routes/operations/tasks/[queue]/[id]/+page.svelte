<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { fetchAudit, fetchTaskDetail, requeueTask } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AuditEventRecord, QueueTaskDetailPayload } from '$lib/types';

  let loading = true;
  let actionLoading = false;
  let error = '';
  let success = '';
  let detail: QueueTaskDetailPayload | null = null;
  let audits: AuditEventRecord[] = [];
  let lastRouteKey = '';

  onMount(async () => {
    await loadDetail();
  });

  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${$page.params.queue}:${$page.params.id}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastRouteKey && !$targetState.session.checking) {
    lastRouteKey = routeKey;
    void loadDetail();
  }

  async function loadDetail(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !$page.params.queue || !$page.params.id) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      [detail, audits] = await Promise.all([
        fetchTaskDetail($selectedTarget, $page.params.queue, $page.params.id),
        fetchAudit($selectedTarget, {
          limit: 30,
          action: 'requeue_task',
          targetType: 'queue_task',
          targetId: $page.params.id
        })
      ]);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load task detail';
    } finally {
      loading = false;
    }
  }

  async function rerunTask(): Promise<void> {
    if (!$selectedTarget || !detail?.item) {
      return;
    }
    actionLoading = true;
    error = '';
    success = '';
    try {
      await requeueTask($selectedTarget, {
        queue: detail.item.queue,
        task_id: detail.item.id
      });
      success = 'Task rerun requested on the selected target.';
      await loadDetail();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to rerun task';
    } finally {
      actionLoading = false;
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Operations / Task"
    title={$page.params.id}
    description="Inspect retry context, payload, queue pressure, and rerun status for this worker task."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect task detail.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading task detail...</div>
  {:else if detail}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}

    <div class="grid-2" style="align-items: start;">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Task</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Execution and retry context</h2>
          </div>
          <div class="runtime-field-status">
            <StatusBadge tone={detail.item.state === 'active' ? 'brand' : detail.item.state === 'archived' || detail.item.state === 'retry' ? 'warning' : 'muted'} label={detail.item.state} />
            {#if detail.item.is_orphaned}
              <StatusBadge tone="danger" label="orphaned" />
            {/if}
            <StatusBadge tone={audits.length > 0 ? 'brand' : 'muted'} label={`${audits.length} reruns`} />
          </div>
        </div>

        <div class="toolbar">
          <div class="debug-block">
            <div class="sidebar-label">Type</div>
            <div>{detail.item.type}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Queue</div>
            <div>{detail.item.queue}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Retries</div>
            <div>{detail.item.retried ?? 0} / {detail.item.max_retry ?? 0}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Last failed</div>
            <div>{detail.item.last_failed_at ? new Date(detail.item.last_failed_at).toLocaleString() : 'n/a'}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Next process</div>
            <div>{detail.item.next_process_at ? new Date(detail.item.next_process_at).toLocaleString() : 'n/a'}</div>
          </div>
        </div>

        <div class="inline-group" style="justify-content: flex-end;">
          <a class="button-ghost" href={`/operations/queues/${encodeURIComponent(detail.item.queue)}`}>Open queue</a>
          <a class="button-ghost" href={`/operations/tasks?queue=${encodeURIComponent(detail.item.queue)}`}>Back to filtered tasks</a>
          <button class="button" type="button" disabled={actionLoading} on:click={rerunTask}>{actionLoading ? 'Requeueing...' : 'Run task now'}</button>
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Linked entities</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Conversation, user, and queue pressure</h2>
          </div>
        </div>

        <div class="toolbar">
          <div class="debug-block">
            <div class="sidebar-label">Conversation</div>
            {#if detail.item.conversation_id}
              <a href={`/conversations/${detail.item.conversation_id}${detail.item.user_id ? `?user_id=${encodeURIComponent(detail.item.user_id)}` : ''}`}>{detail.item.conversation_id}</a>
            {:else}
              <div>n/a</div>
            {/if}
          </div>
          <div class="debug-block">
            <div class="sidebar-label">User</div>
            {#if detail.item.user_id}
              <a href={`/users/${detail.item.user_id}`}>{detail.item.user_id}</a>
            {:else}
              <div>n/a</div>
            {/if}
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Queue latency</div>
            <div>{detail.queue_info?.latency_ms ?? 0} ms</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Queue pending</div>
            <div>{detail.queue_info?.pending ?? 0}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Queue archived</div>
            <div>{detail.queue_info?.archived ?? 0}</div>
          </div>
        </div>
      </section>
    </div>

    <div class="grid-2" style="align-items: start;">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Payload</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Decoded task payload and error detail</h2>
          </div>
        </div>
        <div class="grid-2" style="align-items: start;">
          <div class="debug-block">
            <div class="sidebar-label">Payload</div>
            <JsonView value={detail.item.payload ?? {}} />
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Last error</div>
            <div class="message-body">{detail.item.last_error || 'No error captured for this task.'}</div>
          </div>
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Rerun Audit</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Recorded rerun actions for this queue task</h2>
          </div>
          <a class="button-ghost" href={`/audit?action=requeue_task&target_type=queue_task&target_id=${encodeURIComponent(detail.item.id)}`}>Open audit</a>
        </div>
        {#if audits.length === 0}
          <div class="empty-state">No admin rerun actions have been recorded for this queue task yet.</div>
        {:else}
          <div class="toolbar">
            {#each audits as event}
              <div class="debug-block">
                <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                  <strong>{event.action}</strong>
                  <StatusBadge tone="brand" label={event.created_at ? new Date(event.created_at).toLocaleString() : 'n/a'} />
                </div>
                <div class="helper-text">{event.actor_type ?? 'admin'} · {event.actor_id ?? 'unknown'}</div>
                <div style="margin-top: 10px;">
                  <JsonView value={event.payload ?? {}} emptyLabel="No audit payload" />
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</section>
