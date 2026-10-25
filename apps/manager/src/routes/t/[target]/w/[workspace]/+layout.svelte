<script lang="ts">
  import WorkspacePicker from '#lib/components/workspaces/WorkspacePicker.svelte';
  import type { Snippet } from 'svelte';
  import { page } from '$app/state';
  import { workspaceURL } from '#lib/scope/routes.ts';
  import type { LayoutData } from './$types';
  let { data, children }: { data: LayoutData; children: Snippet } = $props();
  let tabs: HTMLElement | undefined = $state();
  $effect(() => {
    void page.url.pathname;
    const active = tabs?.querySelector<HTMLAnchorElement>('a[aria-current]');
    if (tabs && active) tabs.scrollLeft = Math.max(0, active.offsetLeft - tabs.offsetLeft);
  });
  let settings = $derived(workspaceURL(data.routeTarget.id, page.params.workspace!));
  let access = $derived(settings.replace(/settings$/, 'access'));
  let memory = $derived(settings.replace(/settings$/, 'memory'));
  let keys = $derived(settings.replace(/settings$/, 'keys'));
  let lifecycle = $derived(settings.replace(/settings$/, 'lifecycle'));
  let conversations = $derived(settings.replace(/settings$/, 'conversations'));
</script>

<div class="page-stack">
  {#if data.workspaceResult.value}
    <div class="scope-label"><span>Workspace</span>
      {#key `${data.routeTarget.id}:${data.workspaceResult.value.id}`}<WorkspacePicker scope={{ target: data.routeTarget, workspace: data.workspaceResult.value }} />{/key}
      <span class="scope-owner">Owner: {data.workspaceResult.value.user_id}</span>
    </div>
    <nav bind:this={tabs} class="workspace-tabs" aria-label="Workspace sections">
      <a href={memory} aria-current={page.url.pathname.startsWith(memory) ? 'page' : undefined}>Memory documents</a>
      <a href={keys} aria-current={page.url.pathname.startsWith(keys) ? 'page' : undefined}>Key memories</a>
      <a href={conversations} aria-current={page.url.pathname.startsWith(conversations) ? 'page' : undefined}>Conversations</a>
      <a href={settings} aria-current={page.url.pathname === settings ? 'page' : undefined}>Workspace settings</a>
      <a href={access} aria-current={page.url.pathname.startsWith(access) ? 'page' : undefined}>Agents with access</a>
      <a href={lifecycle} aria-current={page.url.pathname.startsWith(lifecycle) ? 'page' : undefined}>Memory service</a>
    </nav>
  {/if}
  {@render children()}
</div>

<style>
  .workspace-tabs { display: flex; gap: 20px; overflow-x: auto; border-bottom: 1px solid var(--border-subtle); }
  a { display: flex; align-items: center; min-height: 44px; flex-shrink: 0; padding: 0 2px; color: var(--text-muted); }
  a[aria-current] { color: var(--brand); border-bottom: 2px solid var(--brand); }
  .scope-label { display: grid; grid-template-columns: auto minmax(0, 280px) minmax(0, 1fr); align-items: center; gap: 4px 12px; color: var(--text-muted); font-size: var(--font-size-sm); }
  .scope-owner { overflow-wrap: anywhere; }
  @media (max-width: 600px) { .scope-label { grid-template-columns: auto minmax(0, 1fr); } .scope-owner { grid-column: 2; } }
</style>
