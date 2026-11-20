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
  <PageHeader title="Conversations" />
  <form method="GET" class="filter-bar">
    <TextInputField name="q" label="Filter conversations" bind:value={query} placeholder="Conversation ID, provider or status" maxlength={1024} />
    <Button class="btn-secondary" type="submit">Apply</Button>
  </form>
  <WorkspaceReadStatus failure={data.records.failure} />
  {#if data.records.value}
    <div class="record-list">
      <div class="record-list-header"><span>{records.length} conversations</span></div>
      {#each records as item (item.conversation_id)}
        <a class="record-row" href={workspaceConversationURL(base, item.conversation_id)}>
          <span class="row-head"><strong class="mono">{item.conversation_id}</strong><span class="row-time">{new Date(item.updated_at).toLocaleString()}</span></span>
          <MetadataRow items={[item.provider, `${item.message_count} messages`, `${item.total_tokens} tokens`, item.finalize_status || 'unknown']} />
        </a>
      {:else}
        <p class="record-list-empty">No conversations match this view.</p>
      {/each}
    </div>
  {/if}
  <WorkspacePagination path={`${base}/conversations`} query={data.query} after={data.after} next={data.records.value?.next_cursor} invalid={data.invalid} label="Conversations" />
</section>

<style>
  .filter-bar { display: flex; gap: 12px; align-items: end; flex-wrap: wrap; }
  .filter-bar :global(.field) { flex: 1; min-width: min(100%, 240px); }
  .row-head { display: flex; align-items: baseline; justify-content: space-between; gap: 4px 16px; flex-wrap: wrap; min-width: 0; }
  .row-time { color: var(--text-muted); font-size: var(--font-size-sm); white-space: nowrap; }
</style>
