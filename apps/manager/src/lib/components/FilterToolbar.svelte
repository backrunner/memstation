<script lang="ts">
  import type { Snippet } from 'svelte';
  import { SlidersHorizontal } from '@lucide/svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';

  let {
    children,
    filters,
    actions,
    activeCount = 0,
    summary = ''
  }: {
    children: Snippet;
    filters?: Snippet;
    actions?: Snippet;
    activeCount?: number;
    summary?: string;
  } = $props();
</script>

<div class="filter-toolbar">
  {#snippet main(trigger?: Snippet)}
  <div class="filter-toolbar-main">
    <div class="filter-toolbar-primary">{@render children()}</div>
    <div class="filter-toolbar-actions">
      {@render trigger?.()}
      {@render actions?.()}
    </div>
  </div>
  {/snippet}
  {#if filters}
    <CollapsibleSection title={`Filters${activeCount > 0 ? ` (${activeCount})` : ''}`} variant="toolbar" contentClass="filter-toolbar-options">
      {#snippet header(trigger)}{@render main(trigger)}{/snippet}
      {#snippet triggerContent()}
        <SlidersHorizontal size={14} aria-hidden="true" /> Filters{activeCount > 0 ? ` (${activeCount})` : ''}
      {/snippet}
      {@render filters()}
    </CollapsibleSection>
  {:else}
    {@render main()}
  {/if}
  {#if summary}<p class="helper-text filter-toolbar-summary">{summary}</p>{/if}
</div>

<style>
  .filter-toolbar { display: grid; gap: 12px; min-width: 0; }
  .filter-toolbar-main { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 8px; }
  .filter-toolbar-primary { flex: 1 1 180px; min-width: 0; }
  .filter-toolbar-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .filter-toolbar-summary { margin: 0; }
  @media (max-width: 480px) {
    .filter-toolbar-primary { flex-basis: 100%; }
    .filter-toolbar-actions { width: 100%; }
  }
</style>
