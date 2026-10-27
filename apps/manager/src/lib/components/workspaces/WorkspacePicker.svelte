<script lang="ts">
  import { onDestroy } from 'svelte';
  import { page } from '$app/state';
  import { afterNavigate } from '$app/navigation';
  import { Popover } from 'bits-ui';
  import { Check, ChevronDown, X } from '@lucide/svelte';
  import { browseWorkspaces } from '#lib/api/client.ts';
  import { switchWorkspaceURL, workspaceDirectoryURL, type ScopeContext } from '#lib/scope/routes.ts';
  import type { WorkspacePage } from '#lib/types.ts';
  import Button from '#lib/components/ui/Button.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';

  let { scope }: { scope: ScopeContext } = $props();
  const id = $props.id();
  let open = $state(false);
  let query = $state('');
  let searched = $state('');
  let result: WorkspacePage | null = $state(null);
  let error = $state('');
  let busy = $state(false);
  let request = 0;
  let after = $state('');
  onDestroy(() => { request++; });
  afterNavigate(() => { if (open) setOpen(false); });
  async function load(cursor = ''): Promise<void> {
    const id = ++request;
    busy = true; error = ''; result = null; after = cursor;
    const term = query.trim();
    try {
      const value = await browseWorkspaces(scope.target, term, cursor);
      if (id === request && open) { result = value; searched = term; }
    } catch (cause) { if (id === request && open) error = cause instanceof Error ? cause.message : 'Unable to load workspaces.'; }
    finally { if (id === request) busy = false; }
  }
  function setOpen(value: boolean): void {
    open = value;
    if (value) { query = ''; void load(); } else { request++; busy = false; result = null; }
  }
</script>

<Popover.Root {open} onOpenChange={setOpen}>
  <Popover.Trigger class="select-trigger workspace-trigger" aria-label={`Switch workspace: ${scope.workspace.name}`} title={scope.workspace.name}>
    <span class="workspace-name">{scope.workspace.name}</span>
    <ChevronDown size={16} strokeWidth={1.75} class="select-chevron" aria-hidden="true" />
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content role="dialog" class="workspace-picker" align="start" sideOffset={6} collisionPadding={12} aria-labelledby={`${id}-title`} aria-describedby={`${id}-description`}
      onOpenAutoFocus={(event) => { event.preventDefault(); document.getElementById(`${id}-search`)?.focus({ preventScroll: true }); }}>
      <div class="picker-header"><h2 id={`${id}-title`}>Switch workspace</h2><Popover.Close class="btn-ghost btn-icon" aria-label="Close"><X size={16} strokeWidth={1.75} aria-hidden="true" /></Popover.Close></div>
      <form class="picker-search" onsubmit={(event) => { event.preventDefault(); void load(); }}>
        <TextInputField inputId={`${id}-search`} label="Find a workspace" hideLabel bind:value={query} maxlength={120} placeholder="Search name or owner" autocomplete="off" />
        <Button type="submit" class="btn-secondary" disabled={busy}>Search</Button>
      </form>
      {#if busy}<p class="picker-message" role="status">Loading workspaces…</p>{/if}
      {#if error}<div class="picker-message"><p class="error-state" role="alert">{error}</p><Button class="btn-secondary" onclick={() => load()}>Retry</Button></div>{/if}
      {#if result}
        <nav class="workspace-results" aria-label="Available workspaces">
          {#each result.items as workspace (workspace.id)}
            <a href={workspace.id === scope.workspace.id ? `${page.url.pathname}${page.url.search}` : switchWorkspaceURL(page.url, scope.target.id, workspace.id)} onclick={(event) => { if (workspace.id === scope.workspace.id) { event.preventDefault(); setOpen(false); } }} aria-current={workspace.id === scope.workspace.id ? 'true' : undefined}>
              <span class="workspace-option"><strong>{workspace.name}</strong><span class="helper-text">Owner: {workspace.user_id}</span></span>
              {#if workspace.id === scope.workspace.id}<Check size={16} strokeWidth={1.75} aria-hidden="true" /><span class="sr-only">Current workspace</span>{/if}
            </a>
          {:else}<p class="picker-message">No workspaces match this search.</p>{/each}
        </nav>
        {#if after || result.next_cursor}<div class="picker-pages">
          {#if after}<Button class="btn-secondary" onclick={() => { query = searched; void load(); }}>First page</Button>{/if}
          {#if result.next_cursor}<Button class="btn-secondary" onclick={() => { query = searched; void load(result!.next_cursor); }}>Next workspaces</Button>{/if}
        </div>{/if}
      {/if}
      <div class="picker-footer"><p id={`${id}-description`}>Switching opens this section’s list and keeps its filters.</p><a class="text-link" href={workspaceDirectoryURL(scope.target.id)}>All workspaces</a></div>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>

<style>
  :global(.workspace-trigger) { width: min(100%, 280px); min-width: 0; min-height: 44px; gap: 16px; }
  .workspace-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  :global(.workspace-picker) { width: min(360px, calc(100vw - 24px)); max-height: min(520px, var(--bits-popover-content-available-height, calc(100dvh - 24px))); overflow-y: auto; overscroll-behavior: contain; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface); box-shadow: var(--shadow-lg); z-index: 100; }
  .picker-header { display: flex; align-items: center; justify-content: space-between; padding: 8px 8px 0 12px; gap: 12px; }
  h2 { margin: 0; font-size: var(--font-size-md); font-weight: 600; }
  .picker-search { display: flex; align-items: end; gap: 8px; padding: 8px 12px 12px; }
  .picker-search :global(.field) { flex: 1; min-width: 0; }
  .workspace-results { border-block: 1px solid var(--border-subtle); padding: 4px; }
  .workspace-results a { display: flex; align-items: center; gap: 12px; padding: 10px 8px; min-height: 52px; color: var(--text); border-radius: var(--radius-sm); }
  .workspace-results a:hover, .workspace-results a:focus-visible { background: var(--surface-hover); }
  .workspace-results a[aria-current] { background: var(--surface-muted); }
  .workspace-option { display: grid; gap: 2px; flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .workspace-results a :global(svg) { flex-shrink: 0; color: var(--brand); }
  .picker-message { margin: 0; padding: 16px 12px; color: var(--text-muted); }
  .picker-pages { display: flex; flex-wrap: wrap; gap: 8px; padding: 12px 12px 0; }
  .picker-footer { display: grid; gap: 4px; padding: 8px 12px; }
  .picker-footer p { margin: 0; font-size: var(--font-size-xs); color: var(--text-muted); }
  .picker-footer a { display: flex; align-items: center; min-height: 44px; width: fit-content; }
</style>
