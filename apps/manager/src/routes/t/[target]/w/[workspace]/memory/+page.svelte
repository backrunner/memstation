<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import { page } from '$app/state';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import Button from '#lib/components/Button.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL, workspaceMemoryURL, workspaceDocumentKinds } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let query = $derived(data.query); let kind = $derived(data.kind);
  const kinds = [{ value: '', label: 'All document types' }, ...workspaceDocumentKinds.map(value => ({ value, label: value.replaceAll('_', ' ') }))];
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
  let records = $derived(data.records.value?.items ?? []);
  function pageURL(after = ''): string {
    const params = new URLSearchParams({ q: data.query, kind: data.kind });
    if (after) params.set('after', after);
    const generation = after ? data.records.value?.generation_id : data.generation;
    if (generation) params.set('generation_id', generation);
    return `${base}/memory?${params}`;
  }
</script>

<section class="page-stack">
  <PageHeader title="Memory documents" description={`Browse memory documents in ${data.workspaceResult.value?.name ?? 'this workspace'}.`} />
  <a class="text-link" href={`${base}/memory/search`}>Search memory and fact history</a>
  <form method="GET" class="filter-bar">
    <TextInputField name="q" label="Filter documents" bind:value={query} placeholder="Content or document ID" maxlength={1024} />
    <SelectField name="kind" label="Document type" items={kinds} bind:value={kind} />
    {#if data.generation}<input type="hidden" name="generation_id" value={data.generation} />{/if}
    <Button class="btn-secondary" type="submit">Apply</Button>
  </form>
  <WorkspaceReadStatus failure={data.records.failure} />
  {#if data.after}<a class="text-link" href={pageURL()}>First page</a>{/if}
  {#if data.records.value}
    <p class="helper-text">{records.length} shown in this memory generation. New edits may move records between pages.</p>
    {#if data.generation}<p class="helper-text">Viewing generation {data.generation}. <a href={`${base}/memory`}>Return to active memory</a></p>{/if}
    {#if records.length === 0}<p class="notice-card">No memory documents match this view.</p>{/if}
    <div class="list-table">
      {#each records as item (`${item.type}:${item.id}`)}
        <a class="record-row" href={workspaceMemoryURL(base, item.type, item.id, item.generation_id || data.generation)}>
          <span class="preview">{item.content}</span>
          <MetadataRow items={[item.type, item.id]} />
        </a>
      {/each}
    </div>
    {#if data.records.value.next_cursor}<a class="text-link page-link" href={pageURL(data.records.value.next_cursor)}>Next documents</a>{/if}
  {/if}
</section>

<style>
  .page-link { display: inline-flex; align-items: center; min-height: 44px; }
  .filter-bar { display: flex; gap: 12px; align-items: end; flex-wrap: wrap; }
  .filter-bar :global(.field) { flex: 1; min-width: min(100%, 220px); }
  .record-row { display: grid; gap: 8px; padding: 16px 0; border-bottom: 1px solid var(--border-subtle); overflow-wrap: anywhere; }
  .preview { display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; white-space: pre-wrap; }
</style>
