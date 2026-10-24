<script lang="ts">
  import { invalidate } from '$app/navigation';
  import Button from '#lib/components/Button.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { workspaceDirectoryURL, workspaceURL, workspaceBaseURL } from '#lib/scope/routes.ts';
  import { workspaceDestination } from '#lib/scope/navigation.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceDirectoryURL(data.routeTarget.id));
  let nextURL = $derived(`${base}?${new URLSearchParams({ ...data.filters, ...(data.destination ? { open: data.destination } : {}), after: data.directory.value?.next_cursor ?? '' })}`);
</script>

<section class="page-stack">
  <PageHeader title="Workspaces" subtitle={data.routeTarget.label} />
  <p class="panel-meta">Manage existing Personal workspaces and the permissions available to their agents. Project and group workspaces are not available yet.</p>
  {#if data.destination}<p class="notice-card">Choose the Personal workspace to browse. Each workspace keeps its owner and records separate.</p>{/if}
  {#key JSON.stringify(data.filters)}
    <form class="workspace-search" action={base} method="GET">
      <TextInputField label="Search workspaces" name="q" value={data.filters.q} maxlength={120} placeholder="Workspace name or owner" />
      <TextInputField label="Owner ID" name="user_id" value={data.filters.user_id} maxlength={256} placeholder="All owners" />
      {#if data.destination}<input type="hidden" name="open" value={data.destination} />{/if}
      <Button type="submit" class="btn-secondary">Search</Button>
    </form>
  {/key}
  {#if data.directory.failure}
    <div class="error-state" role="alert">{data.directory.failure} <Button class="btn-secondary" onclick={() => invalidate('workspace:directory')}>Retry</Button></div>
  {:else if data.directory.value}
    <div>
      {#each data.directory.value.items as workspace (workspace.id)}
        <a class="workspace-row" href={data.destination ? workspaceDestination(workspaceBaseURL(data.routeTarget.id, workspace.id), data.destination)! : workspaceURL(data.routeTarget.id, workspace.id)}>
          <div><strong>{workspace.name}</strong><span class="helper-text">Owner: {workspace.user_id}</span></div>
          <span class="panel-meta">Personal{workspace.status !== 'active' ? `, ${workspace.status}` : ''}</span>
        </a>
      {:else}
        <p class="panel-meta">No workspaces match these filters. A Personal workspace is provisioned when an owner first uses an agent or credential; existing data is migrated automatically.</p>
      {/each}
    </div>
    <div class="pagination">
      {#if data.filters.after}<a class="text-link" href={`${base}?${new URLSearchParams({ ...data.filters, ...(data.destination ? { open: data.destination } : {}), after: '' })}`}>First page</a>{/if}
      {#if data.directory.value.next_cursor}<a class="text-link" href={nextURL}>Next workspaces</a>{/if}
    </div>
  {/if}
</section>

<style>
  .workspace-search { display: grid; grid-template-columns: minmax(160px, 1fr) minmax(140px, 220px) auto; gap: 12px; align-items: end; }
  .workspace-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px 4px; border-bottom: 1px solid var(--border-subtle); }
  .workspace-row:hover { background: var(--surface-hover); }
  .workspace-row > div { min-width: 0; overflow-wrap: anywhere; }
  .workspace-row .helper-text { display: block; margin-top: 4px; }
  .workspace-row > span { flex-shrink: 0; }
  .pagination { display: flex; gap: 20px; }
  @media (max-width: 600px) { .workspace-search { grid-template-columns: 1fr auto; } .workspace-search :global(.field:first-child) { grid-column: 1 / -1; } .workspace-row { align-items: flex-start; } }
</style>
