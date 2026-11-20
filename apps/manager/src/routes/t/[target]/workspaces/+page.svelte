<script lang="ts">
  import { createWorkspace } from '#lib/api/client.ts';
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import { Dialog } from 'bits-ui';
  import { Plus, X } from '@lucide/svelte';
  import { invalidate } from '$app/navigation';
  import Button from '#lib/components/ui/Button.svelte';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import { workspaceDirectoryURL, workspaceURL, workspaceBaseURL } from '#lib/scope/routes.ts';
  import { workspaceDestination } from '#lib/scope/navigation.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceDirectoryURL(data.routeTarget.id));
  let nextURL = $derived(`${base}?${new URLSearchParams({ ...data.filters, ...data.destinationFields, after: data.directory.value?.next_cursor ?? '' })}`);

  let newOwner = $state(''); let newName = $state(''); let newKind = $state('group'); let busy = $state(false); let createError = $state(''); let createOpen = $state(false);
  async function create(event: SubmitEvent) { event.preventDefault(); busy=true;createError='';try { await createWorkspace(data.routeTarget,newOwner.trim(),newName.trim(),newKind);newName='';createOpen=false;await invalidate('workspace:directory'); } catch(cause) {createError=cause instanceof Error?cause.message:'Unable to create workspace.';} finally {busy=false;} }
</script>

<section class="page-stack">
  <Dialog.Root bind:open={createOpen}>
    <PageHeader title="Workspaces" subtitle={data.routeTarget.label}>
      <svelte:fragment slot="actions">
        <Dialog.Trigger class="btn-primary" onclick={() => (createError = '')}><Plus size={15} aria-hidden="true" /> New workspace</Dialog.Trigger>
      </svelte:fragment>
    </PageHeader>
    <Dialog.Portal>
      <Dialog.Overlay class="dialog-overlay" />
      <Dialog.Content class="dialog-card create-workspace-dialog" escapeKeydownBehavior={busy ? 'ignore' : 'close'} interactOutsideBehavior={busy ? 'ignore' : 'close'}>
        <form class="page-stack" onsubmit={create}>
          <div class="page-header">
            <Dialog.Title class="section-title">Create workspace</Dialog.Title>
            <Dialog.Close class="btn-ghost btn-icon" aria-label="Close" disabled={busy}><X size={16} aria-hidden="true" /></Dialog.Close>
          </div>
          <TextInputField label="New workspace owner ID" bind:value={newOwner} required maxlength={256} />
          <TextInputField label="Workspace name" bind:value={newName} required maxlength={120} />
          <SelectField label="Workspace kind" bind:value={newKind} items={[{value:'group',label:'Group'},{value:'project',label:'Project'}]} />
          {#if createError}<p class="error-state" role="alert">{createError}</p>{/if}
          <div class="form-actions">
            <Dialog.Close class="btn-secondary" disabled={busy}>Cancel</Dialog.Close>
            <Button type="submit" class="btn-primary" disabled={busy}>{busy ? 'Creating…' : 'Create workspace'}</Button>
          </div>
        </form>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
  {#if data.destination}<p class="notice-card">Choose a workspace to continue.</p>{/if}
  {#key JSON.stringify(data.filters)}
    <form class="workspace-search filter-bar" action={base} method="GET">
      <TextInputField label="Search workspaces" name="q" value={data.filters.q} maxlength={120} placeholder="Workspace name or owner" />
      <TextInputField label="Owner ID" name="user_id" value={data.filters.user_id} maxlength={256} placeholder="All owners" />
      {#each Object.entries(data.destinationFields) as [name,value]}<input type="hidden" {name} {value} />{/each}
      <Button type="submit" class="btn-secondary">Search</Button>
    </form>
  {/key}
  {#if data.directory.failure}
    <div class="error-state" role="alert">{data.directory.failure} <Button class="btn-secondary" onclick={() => invalidate('workspace:directory')}>Retry</Button></div>
  {:else if data.directory.value}
    <div class="record-list">
      <div class="record-list-header">
        <span>{data.directory.value.items.length} workspaces</span>
        {#if data.filters.after}<a href={`${base}?${new URLSearchParams({ ...data.filters, ...data.destinationFields, after: '' })}`}>First page</a>{/if}
      </div>
      {#each data.directory.value.items as workspace (workspace.id)}
        <a class="record-row workspace-row" href={data.destination ? workspaceDestination(workspaceBaseURL(data.routeTarget.id, workspace.id), data.destination) : workspaceURL(data.routeTarget.id, workspace.id)}>
          <div><strong>{workspace.name}</strong><span class="helper-text">Owner: {workspace.user_id}</span></div>
          <span class="workspace-kind" class:is-inactive={workspace.status !== 'active'}>{workspace.kind}{workspace.status !== 'active' ? `, ${workspace.status}` : ''}</span>
        </a>
      {:else}
        <p class="record-list-empty">No workspaces match these filters.</p>
      {/each}
      {#if data.directory.value.next_cursor}<div class="record-list-footer"><a class="text-link pagination-link" href={nextURL}>Next workspaces</a></div>{/if}
    </div>
  {/if}
</section>

<style>
  .workspace-search { display: grid; grid-template-columns: minmax(160px, 1fr) minmax(140px, 260px) auto; gap: 12px; align-items: end; }
  .record-list .workspace-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
  .workspace-row > div { min-width: 0; overflow-wrap: anywhere; }
  .workspace-row .helper-text { display: block; margin-top: 2px; }
  .workspace-kind { flex-shrink: 0; color: var(--text-muted); font-size: var(--font-size-sm); text-transform: capitalize; }
  .workspace-kind.is-inactive { color: var(--warning); }
  .pagination-link { display: inline-flex; align-items: center; min-height: 44px; }
  :global(.create-workspace-dialog) { max-width: 480px; }
  @media (max-width: 600px) { .workspace-search { grid-template-columns: 1fr auto; } .workspace-search :global(.field:first-child) { grid-column: 1 / -1; } .record-list .workspace-row { align-items: flex-start; } }
</style>
