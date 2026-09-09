<script lang="ts">
  import type { Snippet } from 'svelte';
  import { SlidersHorizontal } from 'lucide-svelte';

  const id = $props.id();
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
  let expanded = $state(false);
</script>

<div class="filter-toolbar">
  <div class="filter-toolbar-main">
    <div class="filter-toolbar-primary">{@render children()}</div>
    <div class="filter-toolbar-actions">
      {#if filters}
        <button class="btn-secondary" type="button" aria-expanded={expanded} aria-controls={id} onclick={() => (expanded = !expanded)}>
          <SlidersHorizontal size={14} aria-hidden="true" /> Filters{activeCount > 0 ? ` (${activeCount})` : ''}
        </button>
      {/if}
      {@render actions?.()}
    </div>
  </div>
  {#if filters}
    <div class="filter-toolbar-options" {id} hidden={!expanded}>
      {@render filters()}
    </div>
  {/if}
  {#if summary}<p class="helper-text filter-toolbar-summary">{summary}</p>{/if}
</div>

<style>
  .filter-toolbar { display: grid; gap: 12px; min-width: 0; }
  .filter-toolbar-main { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 8px; }
  .filter-toolbar-primary { flex: 1 1 180px; min-width: 0; }
  .filter-toolbar-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .filter-toolbar-options { padding-top: 12px; border-top: 1px solid var(--border-subtle); display: grid; gap: 12px; }
  .filter-toolbar-summary { margin: 0; }
  @media (max-width: 480px) {
    .filter-toolbar-primary { flex-basis: 100%; }
    .filter-toolbar-actions { width: 100%; }
  }
</style>
