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
  let usage = $derived(settings.replace(/settings$/, 'usage'));
 let lifecycle = $derived(settings.replace(/settings$/, 'lifecycle'));
  let conversations = $derived(settings.replace(/settings$/, 'conversations'));
</script>

<div class="page-stack">
  {#if data.workspaceResult.value}
    <div class="workspace-context">
      <div class="scope-label">
        <span class="scope-caption">Workspace</span>
        {#key `${data.routeTarget.id}:${data.workspaceResult.value.id}`}<WorkspacePicker scope={{ target: data.routeTarget, workspace: data.workspaceResult.value }} />{/key}
        <span class="scope-owner">Owner <strong>{data.workspaceResult.value.user_id}</strong></span>
      </div>
      <nav bind:this={tabs} class="workspace-tabs" aria-label="Workspace sections">
        <a href={memory} aria-current={page.url.pathname.startsWith(memory) ? 'page' : undefined}>Memory documents</a>
        <a href={keys} aria-current={page.url.pathname.startsWith(keys) ? 'page' : undefined}>Key memories</a>
        <a href={conversations} aria-current={page.url.pathname.startsWith(conversations) ? 'page' : undefined}>Conversations</a>
        <a href={lifecycle} aria-current={page.url.pathname.startsWith(lifecycle) ? 'page' : undefined}>Memory service</a>
        <a href={access} aria-current={page.url.pathname.startsWith(access) ? 'page' : undefined}>Agents with access</a>
        <a href={usage} aria-current={page.url.pathname.startsWith(usage) ? 'page' : undefined}>Usage</a>
        <a href={settings} aria-current={page.url.pathname === settings ? 'page' : undefined}>Workspace settings</a>
      </nav>
    </div>
  {/if}
  {@render children()}
</div>

<style>
  .workspace-context { display: grid; gap: 14px; min-width: 0; margin-bottom: 4px; }
  .scope-label { display: flex; align-items: center; gap: 10px 14px; flex-wrap: wrap; min-width: 0; }
  .scope-caption { color: var(--text-muted); font-size: var(--font-size-sm); }
  .scope-label :global(.workspace-trigger) { width: 260px; max-width: 100%; }
  .scope-owner { color: var(--text-muted); font-size: var(--font-size-sm); overflow-wrap: anywhere; }
  .scope-owner strong { color: var(--text-secondary); font-weight: 500; }
  .workspace-tabs { display: flex; gap: 4px; overflow-x: auto; min-width: 0; border-bottom: 1px solid var(--border); scrollbar-width: none; }
  .workspace-tabs::-webkit-scrollbar { display: none; }
  a { position: relative; display: flex; align-items: center; min-height: 40px; flex-shrink: 0; padding: 0 10px; color: var(--text-muted); font-size: var(--font-size-md); font-weight: 500; transition: color 120ms ease; }
  a:hover { color: var(--text); }
  a[aria-current] { color: var(--brand-strong); font-weight: 600; }
  a[aria-current]::after { content: ''; position: absolute; left: 8px; right: 8px; bottom: -1px; height: 2px; border-radius: 1px 1px 0 0; background: var(--brand); }
  a:focus-visible { outline-offset: -2px; }
  @media (max-width: 640px) { .scope-label :global(.workspace-trigger) { width: 100%; } .scope-caption { display: none; } a { min-height: 44px; } }
</style>
