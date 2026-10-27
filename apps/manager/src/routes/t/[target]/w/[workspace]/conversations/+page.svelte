<script lang="ts">
  import MetadataRow from '#lib/components/ui/MetadataRow.svelte';
  import { page } from '$app/state';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import Button from '#lib/components/ui/Button.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import WorkspacePagination from '#lib/components/workspaces/WorkspacePagination.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL, workspaceConversationURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let query = $derived(data.query);
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
  let records = $derived(data.records.value?.items ?? []);
</script>

<section class="page-stack">
  <PageHeader title="Conversations" description={`Browse conversations in ${data.workspaceResult.value?.name ?? 'this workspace'}.`} />
  <form method="GET" class="filter-bar">
    <TextInputField name="q" label="Filter conversations" bind:value={query} placeholder="Conversation ID, provider or status" maxlength={1024} />
    <Button class="btn-secondary" type="submit">Apply</Button>
  </form>
  <WorkspaceReadStatus failure={data.records.failure} />
  {#if data.records.value}
    <p class="helper-text">{records.length} shown, newest first. New activity may move conversations between pages.</p>
    {#if records.length === 0}<p class="notice-card">No conversations match this view.</p>{/if}
    <div class="list-table">
      {#each records as item (item.conversation_id)}
        <a class="record-row" href={workspaceConversationURL(base, item.conversation_id)}>
          <strong>{item.conversation_id}</strong>
          <MetadataRow items={[`${item.message_count} messages`, `${item.total_tokens} tokens`, item.finalize_status || 'unknown']} />
          <MetadataRow items={[item.provider, new Date(item.updated_at).toLocaleString()]} />
        </a>
      {/each}
    </div>
  {/if}
  <WorkspacePagination path={`${base}/conversations`} query={data.query} after={data.after} next={data.records.value?.next_cursor} invalid={data.invalid} label="Conversations" />
</section>

<style>
  .filter-bar { display: flex; gap: 12px; align-items: end; flex-wrap: wrap; }
  .filter-bar :global(.field) { flex: 1; min-width: min(100%, 240px); }
  .record-row { display: grid; gap: 5px; padding: 16px 0; border-bottom: 1px solid var(--border-subtle); overflow-wrap: anywhere; }
</style>
