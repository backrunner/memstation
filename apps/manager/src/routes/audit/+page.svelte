<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { fetchAudit } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AuditEventRecord } from '$lib/types';

  let loading = true;
  let error = '';
  let events: AuditEventRecord[] = [];
  let selected: AuditEventRecord | null = null;
  let limit = 100;
  let actorType = '';
  let action = '';
  let targetType = '';
  let targetId = '';
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
  <div class="hero-card">
    <span class="sidebar-label">Audit</span>
    <h1 class="page-title">Sensitive action history</h1>
    <p class="page-description">
      Track API key changes, memory edits, configuration updates, and tool-driven writes with their associated payloads.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to load audit history.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading audit events...</div>
  {:else}
    <section class="card">
      <div class="filters three-up">
        <label class="field">
          <span class="field-label">Action</span>
          <input class="input" bind:value={action} placeholder="update_memory_document" />
        </label>
        <label class="field">
          <span class="field-label">Target type</span>
          <input class="input" bind:value={targetType} placeholder="summary, key_memory, api_key..." />
        </label>
        <label class="field">
          <span class="field-label">Target id</span>
          <input class="input" bind:value={targetId} placeholder="memory id, task id, api key id..." />
        </label>
        <label class="field">
          <span class="field-label">Actor type</span>
          <input class="input" bind:value={actorType} placeholder="admin, tool, system..." />
        </label>
        <label class="field">
          <span class="field-label">Limit</span>
          <input class="input" type="number" min="1" max="500" bind:value={limit} />
        </label>
        <div class="field" style="align-self: end;">
          <button class="button-secondary" type="button" on:click={loadAudit}>Refresh audit</button>
        </div>
      </div>
    </section>

    <div class="grid-2" style="align-items: start;">
      <section class="table-card">
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Target</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {#each events as event}
                <tr on:click={() => (selected = event)} style="cursor: pointer;">
                  <td>
                    <strong>{event.action}</strong>
                    <div class="subtle-text">{event.actor_type ?? 'system'} · {event.actor_id ?? 'unknown'}</div>
                  </td>
                  <td>{event.target_type} / {event.target_id}</td>
                  <td>{event.created_at ? new Date(event.created_at).toLocaleString() : 'n/a'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="debug-block">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Payload</div>
            <h2 class="page-title" style="font-size: 1.2rem;">Selected event detail</h2>
          </div>
        </div>
        {#if selected}
          <div class="toolbar">
            <div class="inline-group"><strong>Action</strong><span>{selected.action}</span></div>
            <div class="inline-group"><strong>Target</strong><span>{selected.target_type} / {selected.target_id}</span></div>
            <div class="inline-group"><strong>Created</strong><span>{selected.created_at ? new Date(selected.created_at).toLocaleString() : 'n/a'}</span></div>
            <JsonView value={selected.payload ?? {}} emptyLabel="No payload on this event" />
          </div>
        {:else}
          <div class="empty-state">Select an event to inspect its payload.</div>
        {/if}
      </section>
    </div>
  {/if}
</section>
