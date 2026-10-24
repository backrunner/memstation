<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import { page } from '$app/state';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import Button from '#lib/components/Button.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import WorkspacePagination from '#lib/components/workspaces/WorkspacePagination.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let query = $derived(data.query);
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
  let records = $derived(data.records.value?.items ?? []);
</script>
<section class="page-stack">
  <PageHeader title="Key memories" description="Maintain durable preferences and instructions in this workspace." />
  <WorkspaceReadStatus failure={data.records.failure} />
  <div class="key-toolbar"><form method="GET" class="key-filter"><TextInputField name="q" label="Filter key memories" bind:value={query} maxlength={1024} placeholder="Key, value, category or status" /><Button class="btn-secondary" type="submit">Apply</Button></form><a class="btn btn-primary" href={`${base}/keys/new`}>New key memory</a></div>
  {#if data.records.value}
    <p class="helper-text">{records.length} shown, newest first. Edits may move key memories between pages.</p>
    {#if records.length === 0}<p>No key memories match this view.</p>{/if}
    {#each records as item (item.id)}
      <a class="record-row" href={`${base}/keys/${encodeURIComponent(item.id)}`}><strong>{item.key}</strong><span class="preview">{item.value}</span><MetadataRow items={[item.category || 'Uncategorized', item.review_status || 'pending']} /></a>
    {/each}
  {/if}
  <WorkspacePagination path={`${base}/keys`} query={data.query} after={data.after} next={data.records.value?.next_cursor} invalid={data.invalid} label="Key memories" />
</section>
<style>
  .key-toolbar { display: flex; align-items: end; gap: 12px; }
  .key-filter { flex: 1; min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: end; gap: 12px; }
  .key-toolbar > a { flex-shrink: 0; }
  .record-row { display: grid; gap: 8px; padding: 16px 0; border-bottom: 1px solid var(--border-subtle); overflow-wrap: anywhere; }
  .preview { white-space: pre-wrap; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
  @media (max-width: 600px) {
    .key-toolbar { display: grid; grid-template-columns: 1fr auto; }
    .key-filter { display: contents; }
    .key-filter :global(.field) { grid-column: 1 / -1; }
    .key-filter :global(button) { justify-self: start; }
  }
</style>
