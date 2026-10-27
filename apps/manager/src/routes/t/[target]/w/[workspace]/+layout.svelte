<script lang="ts">
  import WorkspacePicker from '#lib/components/workspaces/WorkspacePicker.svelte';
  import type { Snippet } from 'svelte';
  import { page } from '$app/state';
  import { workspaceURL } from '#lib/scope/routes.ts';
  import type { LayoutData } from './$types';
  let { data, children }: { data: LayoutData; children: Snippet } = $props();
  let tabs: HTMLElement | undefined = $state();
  function revealActiveSection() {
    const active = tabs?.querySelector<HTMLAnchorElement>('a[aria-current]');
    if (tabs && active) tabs.scrollLeft = Math.max(0, active.offsetLeft - tabs.offsetLeft);
  }
  $effect(() => {
    void page.url.pathname;
    revealActiveSection();
  });
  $effect(() => {
    if (!tabs) return;
    const observer = new ResizeObserver(revealActiveSection);
    observer.observe(tabs);
    return () => observer.disconnect();
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
    <div class="workspace-context">
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
    </div>
  {/if}
  {@render children()}
</div>

<style>
  .workspace-context { display: flex; align-items: center; gap: 24px; padding: 14px 16px; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface); box-shadow: var(--shadow-xs); min-width: 0; }
  .workspace-tabs { display: flex; gap: 4px; overflow-x: auto; min-width: 0; padding: 3px; scrollbar-width: thin; }
  a { display: flex; align-items: center; justify-content: center; min-height: 36px; flex-shrink: 0; padding: 0 11px; border-radius: var(--radius-sm); color: var(--text-muted); font-size: var(--font-size-sm); transition: color 120ms ease, background 120ms ease; }
  a:hover { background: var(--surface-hover); color: var(--text); }
  a[aria-current] { color: var(--brand-strong); background: var(--brand-soft); font-weight: 500; }
  .scope-label { display: grid; grid-template-columns: auto 1fr; gap: 2px 10px; min-width: 0; flex: 0 0 230px; color: var(--text-muted); font-size: 11px; }
  .scope-label :global(.workspace-trigger) { grid-column: 1 / -1; grid-row: 2; width: 100%; max-width: none; }
  .scope-owner { text-align: right; overflow-wrap: anywhere; }
  @media (max-width: 1300px) { .workspace-context { align-items: stretch; flex-direction: column; gap: 12px; } .scope-label { flex-basis: auto; grid-template-columns: auto minmax(0, 280px) minmax(0, 1fr); align-items: center; font-size: var(--font-size-xs); } .scope-label :global(.workspace-trigger) { grid-column: 2; grid-row: 1; } .scope-owner { text-align: left; } }
  @media (max-width: 640px) { .workspace-context { padding: 12px; gap: 10px; } .scope-label { grid-template-columns: minmax(0, 1fr) auto; gap: 6px; } .scope-label :global(.workspace-trigger) { grid-column: 1 / -1; grid-row: 2; } .scope-owner { text-align: right; font-size: 11px; } a { min-height: 44px; padding: 0 12px; } .workspace-tabs { margin-inline: -4px; } }
</style>
