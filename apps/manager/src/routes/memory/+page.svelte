<svelte:options runes={false} />

<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import Button from '#lib/components/Button.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import FilterToolbar from '#lib/components/FilterToolbar.svelte';
  import FactLineagePanel from '#lib/components/FactLineagePanel.svelte';
  import MemoryWorkspaceNav from '#lib/components/MemoryWorkspaceNav.svelte';
  import MemoryKeywordSignals from '#lib/components/MemoryKeywordSignals.svelte';
  import { memoryDetailHref, memoryRouteKindFromDocumentType } from '#lib/memory/routes.ts';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '#lib/memory/keyword-signals.ts';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { fetchMemoryWorkspace, searchMemories, type MemoryWorkspaceData } from '#lib/memory/workspace.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { SearchMemoriesResult } from '#lib/types.ts';

  let loading = true;
  let searchLoading = false;
  let error = '';
  let query = '';
  let asOf = '';
  let knownAt = '';
  let relationMode = 'neighborhood';
  let entityScope = '';
  let dateFrom = '';
  let dateTo = '';
  let selectedTiers: string[] = [];
  let lastRouteKey = '';
  let workspace: MemoryWorkspaceData | null = null;
  let searchResult: SearchMemoriesResult | null = null;
  let activeUserId: string | undefined;
  let routeKey = '';
  let mounted = false;
  let loadRevision = 0;
  let searchRevision = 0;
  let searchError = '';
  const timeTierOptions = [
    { value: 'active', label: 'Active' },
    { value: 'short_term', label: 'Short term' },
    { value: 'long_term', label: 'Long term' },
    { value: 'archived', label: 'Archived' }
  ];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;

  onMount(() => {
    mounted = true;
    return () => { loadRevision++; searchRevision++; };
  });

  $: if (mounted && routeKey !== lastRouteKey && !$targetState.session.checking) {
    lastRouteKey = routeKey;
    void loadWorkspace();
  }

  async function loadWorkspace(): Promise<void> {
    const revision = ++loadRevision;
    const scope = routeKey;
    searchRevision++;
    searchResult = null;
    searchError = '';
    searchLoading = false;
    if (!$selectedTarget || !$targetState.session.authenticated) {
      workspace = null;
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      const result = await fetchMemoryWorkspace($selectedTarget, activeUserId);
      if (revision === loadRevision && scope === routeKey) workspace = result;
    } catch (cause) {
      if (revision === loadRevision && scope === routeKey) error = cause instanceof Error ? cause.message : 'Failed to load memory workspace';
    } finally {
      if (revision === loadRevision && scope === routeKey) loading = false;
    }
  }

  async function runSearch(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !query.trim() || searchLoading) {
      return;
    }
    const revision = ++searchRevision;
    const scope = routeKey;
    searchLoading = true;
    searchError = '';
    searchResult = null;
    try {
      const result = await searchMemories($selectedTarget, query.trim(), 10, activeUserId, {
        asOf: asOf ? new Date(asOf).toISOString() : undefined,
        knownAt: knownAt ? new Date(knownAt).toISOString() : undefined,
        relationMode: asOf || knownAt ? 'neighborhood' : relationMode, entityScope,
        dateFrom: startOfDayISO(dateFrom),
        dateTo: endOfDayISO(dateTo),
        timeTiers: selectedTiers
      });
      if (revision === searchRevision && scope === routeKey) searchResult = result;
    } catch (cause) {
      if (revision === searchRevision && scope === routeKey) searchError = cause instanceof Error ? cause.message : 'Search failed';
    } finally {
      if (revision === searchRevision && scope === routeKey) searchLoading = false;
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
      <form on:submit|preventDefault={runSearch}>
        <FilterToolbar activeCount={Number(Boolean(asOf)) + Number(Boolean(knownAt)) + Number(Boolean(entityScope)) + Number(relationMode !== 'neighborhood') + Number(Boolean(dateFrom)) + Number(Boolean(dateTo)) + selectedTiers.length} summary={searchResult ? `${searchResult.total} matches` : ''}>
          <TextInputField label="Query" bind:value={query} placeholder="Search memory by topic or question…" />
          {#snippet actions()}
            <Button class="btn-primary" type="submit" disabled={searchLoading || !query.trim()}>{searchLoading ? 'Searching…' : 'Run search'}</Button>
          {/snippet}
          {#snippet filters()}
            <div class="filters two-up">
              <TextInputField label="Facts valid at" bind:value={asOf} type="datetime-local" help="Historical fact validity; time is local." />
              <TextInputField label="Known by" bind:value={knownAt} type="datetime-local" help="Fact versions recorded by this local time. Available from history tracking onward." />
              <SelectField label="Relation search" bind:value={relationMode} disabled={Boolean(asOf || knownAt)} help="Relation traversal applies to current memories; historical searches return fact snapshots." items={[{value:'neighborhood',label:'Related memories'},{value:'evidence',label:'Supporting evidence'},{value:'dependencies',label:'Prerequisites'},{value:'dependents',label:'Downstream dependents'},{value:'conflicts',label:'Conflicting evidence'}]} />
              <TextInputField label="Entity scope" bind:value={entityScope} placeholder="Project or workspace" />
              <TextInputField label="Date from" bind:value={dateFrom} type="date" />
              <TextInputField label="Date to" bind:value={dateTo} type="date" />
            </div>
            <div class="field">
              <span class="field-label">Time tier</span>
              <div class="segmented-control" style="flex-wrap: wrap;">
                {#each timeTierOptions as option}
                  <Button class="segmented-control-item" type="button" data-state={selectedTiers.includes(option.value) ? 'on' : 'off'} aria-pressed={selectedTiers.includes(option.value)} onclick={() => toggleTier(option.value)}>{option.label}</Button>
                {/each}
              </div>
            </div>
          {/snippet}
        </FilterToolbar>
      </form>
      {#if searchError}<div class="error-state" role="alert">{searchError}</div>{/if}
      {#if searchResult && searchResult.memories.length === 0}<div class="empty-state">No memories match this query. Try another query or adjust the filters.</div>{/if}

      {#if searchResult}
        <div class="stack-list data-list">
          {#each searchResult.memories as memory}
            {@const routeKind = memoryRouteKindFromDocumentType(memory.type)}
            <article class="memory-result-card data-list-row">
              <header class="memory-result-head">
                <div class="metadata-row">
                  <span>{memory.type}</span>
                  <span>Score <span class="mono">{memory.effectiveScore?.toFixed?.(3) ?? memory.effectiveScore}</span></span>
                </div>
                {#if routeKind && routeKind !== 'keys'}
                  <a
                    class="btn-ghost btn-sm"
                    href={memoryDetailHref(routeKind, memory.id, activeUserId ?? memory.user_id, $selectedTarget?.id)}
                  >Open</a>
                {/if}
              </header>
              <div class="message-body">{memory.content}</div>
              {#if memory.metadata?.historical_snapshot}<p class="helper-text">Historical fact snapshot. Open shows the current record.</p>{/if}
              {#if memory.metadata?.recall_path}<CollapsibleSection title="Why this memory was recalled"><pre>{JSON.stringify(memory.metadata.recall_path, null, 2)}</pre></CollapsibleSection>{/if}
              <MemoryKeywordSignals
                matchedKeywords={matchedKeywordsFromMetadata(memory.metadata)}
                sourceKeywords={sourceKeywordsFromMetadata(memory.metadata)}
              />
              {#if memory.type === 'fact'}
                <FactLineagePanel lineage={memory.lineage} currentFactId={memory.id} userId={activeUserId} compact />
              {/if}
              {#if memory.time_tier || memory.activity_at}
                <MetadataRow class="mono" items={[memory.time_tier, memory.activity_at && dateTimeLabel(memory.activity_at)]} />
              {/if}
            </article>
          {/each}
        </div>
      {/if}
    </section>

    {#if !searchResult}
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Recent memory</h2>
          </div>
        </div>
        <div class="stack-list data-list">
          {#each (workspace?.memorySummaries ?? []).slice(0, 2) as summary}
            <article class="excerpt-card data-list-row">
              <header class="excerpt-head">
                <strong>{summary.topicLabel ?? 'Summary'}</strong>
                <a class="btn-link" href={memoryDetailHref('summaries', summary.id, activeUserId ?? summary.user_id, $selectedTarget?.id)}>Open</a>
              </header>
              <div class="message-body">{summary.summary}</div>
            </article>
          {/each}
          {#each (workspace?.facts ?? []).slice(0, 1) as fact}
            <article class="excerpt-card data-list-row">
              <header class="excerpt-head">
                <strong>Fact</strong>
                <a class="btn-link" href={memoryDetailHref('facts', fact.id, activeUserId ?? fact.user_id, $selectedTarget?.id)}>Open</a>
              </header>
              <div class="message-body">{fact.content}</div>
            </article>
          {/each}
          {#each (workspace?.genericItems ?? []).slice(0, 1) as item}
            {@const itemRouteKind = memoryRouteKindFromDocumentType(item.type)}
            <article class="excerpt-card data-list-row">
              <header class="excerpt-head">
                <strong>{item.type}</strong>
                {#if itemRouteKind}
                  <a class="btn-link" href={memoryDetailHref(itemRouteKind, item.id, activeUserId ?? item.user_id, $selectedTarget?.id)}>Open</a>
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
    {/if}
  {/if}
</section>

<style>
  .memory-result-card,
  .excerpt-card {
    display: grid;
    gap: 8px;
  }

  .memory-result-head,
  .excerpt-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

</style>
