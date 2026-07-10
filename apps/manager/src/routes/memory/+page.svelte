<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import FactLineagePanel from '$lib/components/FactLineagePanel.svelte';
  import MemoryWorkspaceNav from '$lib/components/MemoryWorkspaceNav.svelte';
  import MemoryKeywordSignals from '$lib/components/MemoryKeywordSignals.svelte';
  import { memoryDetailHref, memoryRouteKindFromDocumentType } from '$lib/memory/routes';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '$lib/memory/keyword-signals';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { fetchMemoryWorkspace, searchMemories, type MemoryWorkspaceData } from '$lib/memory/workspace';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { SearchMemoriesResult } from '$lib/types';

  let loading = true;
  let searchLoading = false;
  let error = '';
  let query = '';
  let dateFrom = '';
  let dateTo = '';
  let selectedTiers: string[] = [];
  let lastRouteKey = '';
  let workspace: MemoryWorkspaceData | null = null;
  let searchResult: SearchMemoriesResult | null = null;
  let activeUserId: string | undefined;
  let routeKey = '';
  let userSuffix = '';
  const timeTierOptions = [
    { value: 'active', label: 'Active' },
    { value: 'short_term', label: 'Short term' },
    { value: 'long_term', label: 'Long term' },
    { value: 'archived', label: 'Archived' }
  ];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: userSuffix = activeUserId ? `?user_id=${encodeURIComponent(activeUserId)}` : '';

  onMount(async () => {
    await loadWorkspace();
  });

  $: if (routeKey !== lastRouteKey && !$targetState.session.checking) {
    lastRouteKey = routeKey;
    void loadWorkspace();
  }

  async function loadWorkspace(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      workspace = await fetchMemoryWorkspace($selectedTarget, activeUserId);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load memory workspace';
    } finally {
      loading = false;
    }
  }

  async function runSearch(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !query.trim()) {
      return;
    }
    searchLoading = true;
    error = '';
    try {
      searchResult = await searchMemories($selectedTarget, query.trim(), 10, activeUserId, {
        dateFrom: startOfDayISO(dateFrom),
        dateTo: endOfDayISO(dateTo),
        timeTiers: selectedTiers
      });
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Search failed';
    } finally {
      searchLoading = false;
    }
  }

  function toggleTier(value: string): void {
    if (selectedTiers.includes(value)) {
      selectedTiers = selectedTiers.filter((item) => item !== value);
      return;
    }
    selectedTiers = [...selectedTiers, value];
  }

  function startOfDayISO(value: string): string | undefined {
    if (!value) {
      return undefined;
    }
    return new Date(`${value}T00:00:00`).toISOString();
  }

  function endOfDayISO(value: string): string | undefined {
    if (!value) {
      return undefined;
    }
    return new Date(`${value}T23:59:59.999`).toISOString();
  }

  function dateTimeLabel(value?: string): string {
    if (!value) {
      return 'n/a';
    }
    return new Date(value).toLocaleString();
  }
</script>

<section class="page-stack">
  <PageHeader
    title="Memory workspace"
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  />

  <MemoryWorkspaceNav current="/memory" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect memory data.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading memory workspace…</div>
  {:else}
    <section class="card page-stack">
      <div class="page-header">
        <div class="page-header-meta">
          <h2 class="section-title">Semantic search</h2>
        </div>
        {#if searchResult}
          <StatusBadge tone="brand" label={`${searchResult.total} matches`} />
        {/if}
      </div>

      <div class="filters two-up">
        <TextInputField
          label="Query"
          bind:value={query}
          placeholder="project roadmap, preferences, recent incident, family details…"
          fieldClass="field-span-2"
        />
        <TextInputField label="Date from" bind:value={dateFrom} type="date" />
        <TextInputField label="Date to" bind:value={dateTo} type="date" />
      </div>
      <div class="inline-group">
        <span class="field-label">Time tier</span>
        <div class="segmented-control" style="flex-wrap: wrap;">
          {#each timeTierOptions as option}
            <button
              class="segmented-control-item"
              type="button"
              data-state={selectedTiers.includes(option.value) ? 'on' : 'off'}
              on:click={() => toggleTier(option.value)}
            >{option.label}</button>
          {/each}
        </div>
        <button
          class="btn-primary btn-sm"
          type="button"
          style="margin-left: auto;"
          disabled={searchLoading || !query.trim()}
          on:click={runSearch}
        >
          {searchLoading ? 'Searching…' : 'Run search'}
        </button>
      </div>

      {#if searchResult}
        <div class="stack-list">
          {#each searchResult.memories as memory}
            {@const routeKind = memoryRouteKindFromDocumentType(memory.type)}
            <article class="memory-result-card">
              <header class="memory-result-head">
                <div class="memory-result-head-main">
                  <StatusBadge tone="muted" label={memory.type} />
                  <StatusBadge tone="brand" label={`effective ${memory.effectiveScore?.toFixed?.(3) ?? memory.effectiveScore}`} />
                  {#if matchedKeywordsFromMetadata(memory.metadata).length > 0}
                    <StatusBadge
                      tone="success"
                      label={`${matchedKeywordsFromMetadata(memory.metadata).length} keyword hit${matchedKeywordsFromMetadata(memory.metadata).length === 1 ? '' : 's'}`}
                    />
                  {/if}
                  {#if memory.lineage}
                    <StatusBadge
                      tone={(memory.lineage.current_head_id ?? memory.id) === memory.id ? 'success' : 'warning'}
                      label={(memory.lineage.current_head_id ?? memory.id) === memory.id ? 'current head' : 'historical'}
                    />
                  {/if}
                </div>
                {#if routeKind && routeKind !== 'keys'}
                  <a
                    class="btn-ghost btn-sm"
                    href={memoryDetailHref(routeKind, memory.id, activeUserId)}
                  >Open</a>
                {/if}
              </header>
              <div class="message-body">{memory.content}</div>
              <MemoryKeywordSignals
                matchedKeywords={matchedKeywordsFromMetadata(memory.metadata)}
                sourceKeywords={sourceKeywordsFromMetadata(memory.metadata)}
              />
              {#if memory.type === 'fact'}
                <FactLineagePanel lineage={memory.lineage} currentFactId={memory.id} userId={activeUserId} compact />
              {/if}
              {#if memory.time_tier || memory.activity_at}
                <div class="helper-text mono">
                  {memory.time_tier ?? ''}{#if memory.time_tier && memory.activity_at} · {/if}{#if memory.activity_at}{dateTimeLabel(memory.activity_at)}{/if}
                </div>
              {/if}
            </article>
          {/each}
        </div>
      {/if}
    </section>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Collections</h2>
          </div>
        </div>
        <div class="list-table">
          <a class="table-row-link" href={`/memory/summaries${userSuffix}`}>
            <span>Summaries</span>
            <span class="mono">{workspace?.memorySummaries.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/impressions${userSuffix}`}>
            <span>Impressions</span>
            <span class="mono">{workspace?.impressions.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/facts${userSuffix}`}>
            <span>Facts</span>
            <span class="mono">{workspace?.facts.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/items${userSuffix}`}>
            <span>Agent memory</span>
            <span class="mono">{workspace?.genericItems.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/keys${userSuffix}`}>
            <span>Key memories</span>
            <span class="mono">{workspace?.keyMemories.length ?? 0}</span>
          </a>
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Recent memory</h2>
          </div>
        </div>
        <div class="stack-list">
          {#each (workspace?.memorySummaries ?? []).slice(0, 2) as summary}
            <article class="excerpt-card">
              <header class="excerpt-head">
                <strong>{summary.topicLabel ?? 'Summary'}</strong>
                <a class="btn-link" href={memoryDetailHref('summaries', summary.id, activeUserId)}>Open</a>
              </header>
              <div class="message-body">{summary.summary}</div>
            </article>
          {/each}
          {#each (workspace?.facts ?? []).slice(0, 1) as fact}
            <article class="excerpt-card">
              <header class="excerpt-head">
                <strong>Fact</strong>
                <a class="btn-link" href={memoryDetailHref('facts', fact.id, activeUserId)}>Open</a>
              </header>
              <div class="message-body">{fact.content}</div>
            </article>
          {/each}
          {#each (workspace?.genericItems ?? []).slice(0, 1) as item}
            {@const itemRouteKind = memoryRouteKindFromDocumentType(item.type)}
            <article class="excerpt-card">
              <header class="excerpt-head">
                <strong>{item.type}</strong>
                {#if itemRouteKind}
                  <a class="btn-link" href={memoryDetailHref(itemRouteKind, item.id, activeUserId)}>Open</a>
                {/if}
              </header>
              <div class="message-body">{item.content}</div>
            </article>
          {/each}
          {#if (workspace?.memorySummaries.length ?? 0) === 0 && (workspace?.facts.length ?? 0) === 0 && (workspace?.genericItems.length ?? 0) === 0}
            <div class="empty-state">This target has not generated memory documents yet.</div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
</section>

<style>
  .memory-result-card,
  .excerpt-card {
    display: grid;
    gap: 8px;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
  }

  .memory-result-head,
  .excerpt-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .memory-result-head-main {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
</style>
