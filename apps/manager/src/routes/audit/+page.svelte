<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { fetchAudit } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AuditEventRecord } from '$lib/types';

  let loading = true;
  let error = '';
  let events: AuditEventRecord[] = [];
  let visibleEvents: AuditEventRecord[] = [];
  let selected: AuditEventRecord | null = null;
  let limit = 100;
  let actorType = '';
  let action = '';
  let targetType = '';
  let targetId = '';
  let dateFrom = '';
  let dateTo = '';
  let lastFilterKey = '';
  let lastLoadedKey = '';

  onMount(async () => {
    await loadAudit();
  });

  $: routeAction = $page.url.searchParams.get('action') ?? '';
  $: routeTargetType = $page.url.searchParams.get('target_type') ?? '';
  $: routeTargetId = $page.url.searchParams.get('target_id') ?? '';
  $: routeActorType = $page.url.searchParams.get('actor_type') ?? '';
  $: routeLimit = Number($page.url.searchParams.get('limit') ?? '100');
  $: filterKey = `${routeAction}:${routeTargetType}:${routeTargetId}:${routeActorType}:${routeLimit}`;
  $: if (filterKey !== lastFilterKey) {
    lastFilterKey = filterKey;
    action = routeAction;
    targetType = routeTargetType;
    targetId = routeTargetId;
    actorType = routeActorType;
    limit = Number.isFinite(routeLimit) && routeLimit > 0 ? routeLimit : 100;
  }

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${action}:${targetType}:${targetId}:${actorType}:${limit}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadedKey && !$targetState.session.checking) {
    lastLoadedKey = loadKey;
    void loadAudit();
  }

  $: visibleEvents = events.filter((event) => {
    const createdAt = event.created_at ? new Date(event.created_at).getTime() : 0;
    const from = dateFrom ? new Date(`${dateFrom}T00:00:00`).getTime() : Number.NEGATIVE_INFINITY;
    const to = dateTo ? new Date(`${dateTo}T23:59:59.999`).getTime() : Number.POSITIVE_INFINITY;
    return createdAt >= from && createdAt <= to;
  });

  $: if (selected && !visibleEvents.some((event) => event.id === selected?.id)) {
    selected = visibleEvents[0] ?? null;
  }

  async function loadAudit(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      events = await fetchAudit($selectedTarget, {
        limit,
        actorType,
        action,
        targetType,
        targetId
      });
      selected = events[0] ?? null;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load audit events';
    } finally {
      loading = false;
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Administer"
    title="Audit"
    description="Track API key changes, memory edits, configuration updates, and tool-driven writes with their associated payloads."
  >
    <div slot="actions">
      <button class="btn-secondary btn-sm" type="button" on:click={loadAudit}>Refresh</button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to load audit history.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading audit events…</div>
  {:else}
    <section class="card page-stack">
      <div class="page-header">
        <div class="page-header-meta">
          <h2 class="section-title">Filters</h2>
          <p class="panel-meta">Refine by action, target, actor, and limit.</p>
        </div>
      </div>
      <div class="filters three-up">
        <TextInputField label="Action" bind:value={action} placeholder="update_memory_document" />
        <TextInputField label="Target type" bind:value={targetType} placeholder="summary, key_memory, api_key…" />
        <TextInputField label="Target ID" bind:value={targetId} placeholder="memory id, task id, api key id…" />
        <TextInputField label="Actor type" bind:value={actorType} placeholder="admin, tool, system…" />
        <TextInputField label="Date from" bind:value={dateFrom} type="date" />
        <TextInputField label="Date to" bind:value={dateTo} type="date" />
        <TextInputField label="Limit" bind:value={limit} type="number" min="1" max="500" />
      </div>
    </section>

    <div class="page-columns">
      <section class="table-card">
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Target</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {#each visibleEvents as event}
                <tr
                  class="is-selectable"
                  class:is-active={selected?.id === event.id}
                  on:click={() => (selected = event)}
                >
                  <td>
                    <strong class="mono">{event.action}</strong>
                    <div class="subtle-text">{event.actor_type ?? 'system'} · {event.actor_id ?? 'unknown'}</div>
                  </td>
                  <td>
                    <div class="mono">{event.target_type}</div>
                    <div class="subtle-text mono">{event.target_id}</div>
                  </td>
                  <td class="subtle-text no-wrap">{event.created_at ? new Date(event.created_at).toLocaleString() : 'n/a'}</td>
                </tr>
              {/each}
              {#if visibleEvents.length === 0}
                <tr>
                  <td colspan="3">
                    <div class="empty-state" style="border: none; padding: 12px 0;">No audit events match the current filter.</div>
                  </td>
                </tr>
              {/if}
            </tbody>
          </table>
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Event detail</h2>
            <p class="panel-meta">Payload captured when this action was performed.</p>
          </div>
        </div>
        {#if selected}
          <dl class="audit-dl">
            <div>
              <dt>Action</dt>
              <dd class="mono">{selected.action}</dd>
            </div>
            <div>
              <dt>Target</dt>
              <dd class="mono">{selected.target_type} / {selected.target_id}</dd>
            </div>
            <div>
              <dt>Actor</dt>
              <dd>{selected.actor_type ?? 'system'} <span class="subtle-text">· {selected.actor_id ?? 'unknown'}</span></dd>
            </div>
            <div>
              <dt>Created</dt>
              <dd>{selected.created_at ? new Date(selected.created_at).toLocaleString() : 'n/a'}</dd>
            </div>
          </dl>
          <div>
            <div class="field-label" style="margin-bottom: 6px;">Payload</div>
            <JsonView value={selected.payload ?? {}} emptyLabel="No payload on this event." />
          </div>
        {:else}
          <div class="empty-state">Select an event to inspect its payload.</div>
        {/if}
      </section>
    </div>
  {/if}
</section>

<style>
  .audit-dl {
    display: grid;
    gap: 8px;
    margin: 0;
  }

  .audit-dl > div {
    display: grid;
    grid-template-columns: 100px minmax(0, 1fr);
    gap: 12px;
    align-items: baseline;
  }

  .audit-dl dt {
    color: var(--text-muted);
    font-size: var(--font-size-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0;
    margin: 0;
  }

  .audit-dl dd {
    margin: 0;
    color: var(--text);
    font-size: var(--font-size-md);
    word-break: break-all;
  }
</style>
