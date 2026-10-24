<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import { SlidersHorizontal } from '@lucide/svelte';
  import { page } from '$app/state';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import Button from '#lib/components/Button.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL, workspaceMemoryURL, workspaceDocumentKinds } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
  let activeCount = $derived(['kind', 'as_of', 'known_at', 'date_from', 'date_to'].filter(key => data.filters[key as keyof typeof data.filters]).length);
  const kinds = [{ value: '', label: 'All types (facts for history)' }, ...[...workspaceDocumentKinds, 'key_memory'].map(value => ({ value, label: value.replaceAll('_', ' ') }))];
</script>

<section class="page-stack">
  <PageHeader title="Search memory" description={`Search ${data.workspaceResult.value?.name ?? 'this workspace'} using its configured memory retrieval service.`} />
  <a class="text-link" href={`${base}/memory`}>Browse memory documents</a>
  {#key JSON.stringify(data.filters)}
    <form method="GET" class="page-stack">
      <div class="search-main">
        <TextInputField name="q" label="Search memory" value={data.filters.q} maxlength={1024} placeholder="A topic, preference or past event" />
        <Button type="submit" class="btn-primary">Search</Button>
      </div>
      {#if data.generation}<input type="hidden" name="generation_id" value={data.generation} />{/if}
      <CollapsibleSection title="Filters" count={activeCount || undefined} variant="toolbar">
        {#snippet icon()}<SlidersHorizontal size={16} />{/snippet}
        <div class="search-filters">
          <SelectField label="Memory type" name="kind" value={data.filters.kind} items={kinds} />
          <TextInputField label="Maximum results" name="top_n" type="number" min={1} max={100} value={data.filters.top_n} required />
          <TextInputField label="Active from" name="date_from" value={data.filters.date_from} placeholder="2026-01-01T00:00:00Z" />
          <TextInputField label="Active until" name="date_to" value={data.filters.date_to} placeholder="2026-12-31T23:59:59Z" />
          <TextInputField label="Fact valid at" name="as_of" value={data.filters.as_of} placeholder="2026-01-01T00:00:00Z" help="When the fact was true. History is available for facts only." />
          <TextInputField label="Known by the system at" name="known_at" value={data.filters.known_at} placeholder="2026-01-02T00:00:00Z" help="What the system had recorded at this time. Later corrections are excluded." />
        </div>
        <p class="helper-text">Use ISO timestamps with Z or an explicit timezone offset. Historical snapshots start from the fact-history migration.</p>
      </CollapsibleSection>
      {#if activeCount}<MetadataRow class="filter-summary" items={[data.filters.kind || (data.historical ? 'Facts' : 'All types'), data.filters.date_from && `Active from ${data.filters.date_from}`, data.filters.date_to && `Active until ${data.filters.date_to}`, data.filters.as_of && `Valid at ${data.filters.as_of}`, data.filters.known_at && `Known at ${data.filters.known_at}`]} />{/if}
      {#if activeCount || data.generation}<a class="text-link" href={`${base}/memory/search?${new URLSearchParams({ q: data.filters.q })}`}>Clear filters and use active memory</a>{/if}
    </form>
  {/key}
  {#if data.generation}<p class="helper-text">Memory generation: {data.generation}</p>{/if}
  <WorkspaceReadStatus failure={data.result.failure || data.workspaceResult.failure} />
  {#if data.result.value}
    {#if data.historical}<p class="notice-card">Historical fact snapshots are read only. Opening a current record may show newer content.</p>{/if}
    <p class="helper-text">{data.result.value.memories.length} ranked results, up to {data.filters.top_n} requested.</p>
    <div class="search-results">
      {#each data.result.value.memories as item (`${item.type}:${item.id}`)}
        <article class="search-result">
          <div class="result-meta"><MetadataRow items={[item.type.replaceAll('_', ' '), item.id]} /><span>Score {Number(item.effective_score ?? item.score ?? 0).toFixed(3)}</span></div>
          <p class="memory-content">{item.content}</p>
          {#if data.historical}
            <MetadataRow items={[`Valid from: ${String(item.metadata?.valid_from ?? 'unspecified')}`, `Valid until: ${String(item.metadata?.valid_to ?? 'open')}`, `Recorded: ${String(item.metadata?.recorded_at ?? 'unavailable')}`]} />
          {/if}
          <a class="text-link record-link" href={item.type === 'key_memory' ? `${base}/keys/${encodeURIComponent(item.id)}` : workspaceMemoryURL(base, item.type, item.id, item.generation_id || data.generation)}>{data.historical ? 'Open current record' : 'Open record'}</a>
        </article>
      {:else}<p class="notice-card">No memories match these filters.</p>{/each}
    </div>
  {:else if !data.submitted && !data.result.failure}<p class="helper-text">Enter a topic to retrieve relevant memory. Use the time filters to inspect past facts.</p>{/if}
</section>

<style>
  .search-main { display: flex; align-items: end; gap: 12px; }
  .search-main :global(.field) { flex: 1; min-width: 0; }
  .search-filters { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; padding: 12px 0; }
  .search-result { padding: 20px 0; border-bottom: 1px solid var(--border-subtle); min-width: 0; }
  .result-meta { display: flex; gap: 12px; flex-wrap: wrap; justify-content: space-between; color: var(--text-muted); font-size: 13px; overflow-wrap: anywhere; }
  .memory-content { white-space: pre-wrap; overflow-wrap: anywhere; margin: 12px 0; }
  .record-link { min-height: 44px; display: inline-flex; align-items: center; }
  @media (max-width: 600px) { .search-filters { grid-template-columns: 1fr; } }
</style>
