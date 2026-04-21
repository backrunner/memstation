<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import MemoryWorkspaceNav from '$lib/components/MemoryWorkspaceNav.svelte';
  import MemoryKeywordSignals from '$lib/components/MemoryKeywordSignals.svelte';
  import { memoryDetailHref } from '$lib/memory/routes';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '$lib/memory/keyword-signals';
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
  <div class="hero-card">
    <span class="sidebar-label">Memory Workspace</span>
    <h1 class="page-title">Memory Workspace</h1>
    <p class="page-description">
      Use the workspace as the operator hub for semantic recall checks, summary QA, fact review, and long-lived key memory management.
    </p>
    {#if activeUserId}
      <div class="helper-text">Scoped to user `{activeUserId}` from the current route query.</div>
    {/if}
  </div>

  <MemoryWorkspaceNav current="/memory" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect memory data from the selected target.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading memory workspace...</div>
  {:else}
    <div class="kpi-grid">
      <div class="kpi-card">
        <span class="kpi-label">Summaries</span>
        <span class="kpi-value">{workspace?.memorySummaries.length ?? 0}</span>
        <span class="kpi-meta">Conversation rollups ready for recall.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Impressions</span>
        <span class="kpi-value">{workspace?.impressions.length ?? 0}</span>
        <span class="kpi-meta">Behavioral or preference signals extracted by analysis.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Facts</span>
        <span class="kpi-value">{workspace?.facts.length ?? 0}</span>
        <span class="kpi-meta">Structured factual statements available to retrieval.</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Key Memories</span>
        <span class="kpi-value">{workspace?.keyMemories.length ?? 0}</span>
        <span class="kpi-meta">Stable operator-managed slots exposed through tools and REST.</span>
      </div>
    </div>

    <section class="card page-stack">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Semantic search</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Test ranked recall against live memory data</h2>
        </div>
        {#if searchResult}
          <StatusBadge tone="brand" label={`${searchResult.total} matches`} />
        {/if}
      </div>

      <div class="filters two-up">
        <TextInputField label="Search Query" bind:value={query} placeholder="project roadmap, preferences, recent incident, family details..." />
        <div class="field" style="align-self: end;">
          <button class="button" type="button" disabled={searchLoading || !query.trim()} on:click={runSearch}>
            {searchLoading ? 'Searching...' : 'Run Semantic Search'}
          </button>
        </div>
      </div>
      <div class="filters two-up">
        <TextInputField label="Date From" bind:value={dateFrom} type="date" />
        <TextInputField label="Date To" bind:value={dateTo} type="date" />
      </div>
      <div class="inline-group" style="flex-wrap: wrap;">
        {#each timeTierOptions as option}
          <button
            class={selectedTiers.includes(option.value) ? 'button-secondary' : 'button-ghost'}
            type="button"
            on:click={() => toggleTier(option.value)}
          >
            {option.label}
          </button>
        {/each}
      </div>

      {#if !searchResult}
        <div class="empty-state">Run a search to inspect semantic score, effective score, and the memory snippets that would be injected.</div>
      {:else}
        <div class="inline-group" style="justify-content: space-between;">
          <span class="helper-text">Query embedding token estimate: {searchResult.queryEmbeddingTokens}</span>
          <span class="helper-text">Top {searchResult.memories.length} items shown</span>
        </div>
        <div class="toolbar">
          {#each searchResult.memories as memory}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <strong>{memory.type}</strong>
                <div class="inline-group" style="gap: 8px;">
                  <span class="subtle-text">effective {memory.effectiveScore?.toFixed?.(3) ?? memory.effectiveScore}</span>
                  {#if matchedKeywordsFromMetadata(memory.metadata).length > 0}
                    <StatusBadge tone="brand" label={`${matchedKeywordsFromMetadata(memory.metadata).length} keyword hit${matchedKeywordsFromMetadata(memory.metadata).length === 1 ? '' : 's'}`} />
                  {/if}
                  {#if memory.type === 'summary' || memory.type === 'impression' || memory.type === 'fact'}
                    <a class="button-ghost" href={memoryDetailHref(memory.type === 'summary' ? 'summaries' : memory.type === 'impression' ? 'impressions' : 'facts', memory.id, activeUserId)}>Open</a>
                  {/if}
                </div>
              </div>
              <div class="message-body">{memory.content}</div>
              <MemoryKeywordSignals
                matchedKeywords={matchedKeywordsFromMetadata(memory.metadata)}
                sourceKeywords={sourceKeywordsFromMetadata(memory.metadata)}
              />
              <div class="helper-text" style="margin-top: 10px;">raw semantic score: {memory.score?.toFixed?.(3) ?? memory.score}</div>
              <div class="helper-text">
                effective: {memory.effectiveScore?.toFixed?.(3) ?? memory.effectiveScore} · tier {memory.time_tier ?? 'n/a'} · activity {dateTimeLabel(memory.activity_at)}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Collections</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Memory stores at a glance</h2>
          </div>
        </div>
        <div class="list-table">
          <a class="table-row-link" href={`/memory/summaries${userSuffix}`}>
            <span>Summaries</span>
            <span>{workspace?.memorySummaries.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/impressions${userSuffix}`}>
            <span>Impressions</span>
            <span>{workspace?.impressions.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/facts${userSuffix}`}>
            <span>Facts</span>
            <span>{workspace?.facts.length ?? 0}</span>
          </a>
          <a class="table-row-link" href={`/memory/keys${userSuffix}`}>
            <span>Key Memories</span>
            <span>{workspace?.keyMemories.length ?? 0}</span>
          </a>
        </div>
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Recent excerpts</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Quick spot-check samples</h2>
          </div>
        </div>
        <div class="toolbar">
          {#each (workspace?.memorySummaries ?? []).slice(0, 2) as summary}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <strong>{summary.topicLabel ?? 'Summary'}</strong>
                <a class="button-ghost" href={memoryDetailHref('summaries', summary.id, activeUserId)}>Open</a>
              </div>
              <div class="message-body">{summary.summary}</div>
            </div>
          {/each}
          {#each (workspace?.facts ?? []).slice(0, 1) as fact}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <strong>Fact</strong>
                <a class="button-ghost" href={memoryDetailHref('facts', fact.id, activeUserId)}>Open</a>
              </div>
              <div class="message-body">{fact.content}</div>
            </div>
          {/each}
          {#if (workspace?.memorySummaries.length ?? 0) === 0 && (workspace?.facts.length ?? 0) === 0}
            <div class="empty-state">This target has not generated summaries or facts yet.</div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
</section>
