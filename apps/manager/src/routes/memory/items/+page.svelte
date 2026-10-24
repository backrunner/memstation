<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import { fetchAdminMemoryDocuments } from '#lib/api/client.ts';
  import MemoryWorkspaceNav from '#lib/components/MemoryWorkspaceNav.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { genericMemoryKinds } from '#lib/memory/workspace.ts';
  import { memoryDetailHref, memoryRouteKindFromDocumentType } from '#lib/memory/routes.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { GenericMemoryDocumentKind, MemoryDocument } from '#lib/types.ts';

  let loading = true;
  let error = '';
  let filter = '';
  let typeFilter = '';
  let lastRouteKey = '';
  let lastRequestedType = '';
  let items: MemoryDocument[] = [];
  let visibleItems: MemoryDocument[] = [];
  let activeUserId: string | undefined;
  let routeKey = '';

  const typeOptions = [
    { value: '', label: 'All agent memory' },
    { value: 'event', label: 'Events' },
    { value: 'task', label: 'Tasks' },
    { value: 'procedure', label: 'Procedures' },
    { value: 'artifact', label: 'Artifacts' },
    { value: 'entity', label: 'Entities' },
    { value: 'agent_action', label: 'Agent actions' }
  ];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: requestedType = normalizeTypeFilter($page.url.searchParams.get('type') ?? '');
  $: if (requestedType !== lastRequestedType) {
    lastRequestedType = requestedType;
    typeFilter = requestedType;
  }
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastRouteKey && !$targetState.session.checking) {
    lastRouteKey = routeKey;
    void loadData();
  }
  $: visibleItems = items.filter((item) => {
    if (typeFilter && item.type !== typeFilter) {
      return false;
    }
    const needle = filter.trim().toLowerCase();
    if (!needle) {
      return true;
    }
    return [item.id, item.type, titleForItem(item), item.content, metadataString(item.metadata, 'status'), metadataString(item.metadata, 'review_status')]
      .join(' ')
      .toLowerCase()
      .includes(needle);
  });

  onMount(async () => {
    await loadData();
  });

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      items = await fetchAdminMemoryDocuments($selectedTarget, {
        kind: genericMemoryKinds,
        userId: activeUserId,
        limit: 300
      });
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load agent memory items';
    } finally {
      loading = false;
    }
  }

  function normalizeTypeFilter(value: string): GenericMemoryDocumentKind | '' {
    return genericMemoryKinds.includes(value as GenericMemoryDocumentKind) ? (value as GenericMemoryDocumentKind) : '';
  }

  function metadataString(metadata: Record<string, unknown> | undefined, key: string): string {
    const value = metadata?.[key];
    return typeof value === 'string' ? value : '';
  }

  function titleForItem(item: MemoryDocument): string {
    return metadataString(item.metadata, 'title') || item.type;
  }

  function typeLabel(value: string): string {
    return typeOptions.find((item) => item.value === value)?.label ?? value;
  }

  function statusTone(value?: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    switch (value) {
      case 'current':
      case 'active':
      case 'completed':
      case 'approved':
        return 'success';
      case 'pending':
      case 'blocked':
        return 'warning';
      case 'rejected':
      case 'failed':
      case 'abandoned':
        return 'danger';
      default:
        return 'muted';
    }
  }

  function dateTimeLabel(value?: string): string {
    return value ? new Date(value).toLocaleString() : 'n/a';
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Inspect / Memory"
    title="Agent Memory"
    description="Durable events, tasks, procedures, artifacts, entities, and agent actions promoted from source conversations."
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  />

  <MemoryWorkspaceNav current="/memory/items" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect agent memory records.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading agent memory records...</div>
  {:else}
    <section class="card page-stack">
      <div class="filters two-up">
        <TextInputField label="Filter" bind:value={filter} placeholder="workflow, tool, artifact, entity, task status..." />
        <SelectField label="Type" bind:value={typeFilter} items={typeOptions} placeholder="All agent memory" />
      </div>
      <div class="inline-group" style="justify-content: space-between; flex-wrap: wrap;">
        <span class="helper-text">{visibleItems.length} of {items.length} agent memory items visible</span>
        {#if typeFilter}
          <StatusBadge tone="brand" label={typeLabel(typeFilter)} />
        {/if}
      </div>

      {#if visibleItems.length === 0}
        <div class="empty-state">No agent memory items match the current filter.</div>
      {:else}
        <div class="stack-list data-list">
          {#each visibleItems as item}
            {@const routeKind = memoryRouteKindFromDocumentType(item.type)}
            {@const status = metadataString(item.metadata, 'status')}
            {@const reviewStatus = metadataString(item.metadata, 'review_status')}
            <article class="agent-memory-card data-list-row">
              <header class="agent-memory-head">
                <div class="agent-memory-head-main">
                  <strong>{titleForItem(item)}</strong>
                  <span class="helper-text mono">{item.id}</span>
                </div>
                <div class="inline-group">
                  <StatusBadge tone="muted" label={item.type} />
                  {#if status}
                    <StatusBadge tone={statusTone(status)} label={status} />
                  {/if}
                  {#if reviewStatus}
                    <StatusBadge tone={statusTone(reviewStatus)} label={reviewStatus} />
                  {/if}
                  {#if routeKind}
                    <a class="btn-ghost btn-sm" href={memoryDetailHref(routeKind, item.id, activeUserId ?? item.user_id, $selectedTarget?.id)}>Open</a>
                  {/if}
                </div>
              </header>
              <div class="message-body">{item.content}</div>
              <div class="agent-memory-meta">
                <span>activity {dateTimeLabel(item.activity_at)}</span>
                <span>confidence {Number(item.metadata?.confidence ?? 0).toFixed(2)}</span>
                <span>{item.related?.length ?? 0} relation{(item.related?.length ?? 0) === 1 ? '' : 's'}</span>
                <span>{item.evidences?.length ?? 0} evidence item{(item.evidences?.length ?? 0) === 1 ? '' : 's'}</span>
              </div>
            </article>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>

<style>
  .agent-memory-card {
    display: grid;
    gap: 8px;
  }

  .agent-memory-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  .agent-memory-head-main {
    display: grid;
    gap: 3px;
    min-width: 0;
  }

  .agent-memory-meta {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    color: var(--text-secondary);
    font-size: 0.78rem;
  }
</style>
