<svelte:options runes={false} />

<script lang="ts">
  import { selectedTarget } from '#lib/stores/targets.ts';
  import StatusBadge from '#lib/components/ui/StatusBadge.svelte';
  import { memoryDetailHref } from '#lib/memory/routes.ts';
  import type { MemoryLineage } from '#lib/types.ts';

  export let lineage: MemoryLineage | undefined;
  export let currentFactId = '';
  export let userId: string | undefined;
  export let compact = false;

  $: hasLineage = Boolean(lineage);
  $: lineagePath = lineage?.lineage_path ?? [];
  $: aliasCount = lineage?.superseded_alias_node_ids?.length ?? 0;
  $: currentHeadId = lineage?.current_head_id ?? lineagePath[lineagePath.length - 1] ?? '';
  $: isCurrentHead = Boolean(currentHeadId && currentFactId && currentHeadId === currentFactId);

  function shortID(value: string): string {
    return value.length > 30 ? `${value.slice(0, 14)}...${value.slice(-10)}` : value;
  }
</script>

{#if hasLineage}
  <div class:lineage-panel={!compact} class:lineage-inline={compact}>
    <div class="lineage-head">
      <div class="lineage-title">
        <span>Lineage</span>
        {#if lineage?.fact_key}
          <span class="mono subtle-text">{lineage.fact_key}</span>
        {/if}
      </div>
      <div class="metadata-row">
        <StatusBadge tone={isCurrentHead ? 'success' : 'warning'} label={isCurrentHead ? 'current head' : 'historical'} />
        {#if lineage?.revision_seq}
          <span>Revision {lineage.revision_seq}</span>
        {/if}
        {#if lineage?.revision_depth}
          <span>Depth {lineage.revision_depth}</span>
        {/if}
      </div>
    </div>

    {#if !compact}
      <div class="lineage-grid">
        <div>
          <div class="sidebar-label">Group</div>
          <div class="mono breakable">{lineage?.lineage_group_id ?? 'n/a'}</div>
        </div>
        <div>
          <div class="sidebar-label">Current Head</div>
          <div class="mono breakable">{currentHeadId || 'n/a'}</div>
        </div>
      </div>
    {/if}

    {#if lineagePath.length > 0}
      <div class="lineage-path" aria-label="fact lineage path">
        {#each lineagePath as factId, index}
          <a
            class:lineage-node={true}
            class:is-active={factId === currentFactId}
            class:is-head={factId === currentHeadId}
            href={memoryDetailHref('facts', factId, userId, $selectedTarget?.id)}
          >
            <span class="lineage-node-index">{index + 1}</span>
            <span class="mono">{shortID(factId)}</span>
          </a>
        {/each}
      </div>
    {:else if !compact}
      <div class="helper-text">No expanded lineage path is attached to this fact yet.</div>
    {/if}

    {#if aliasCount > 0 && !compact}
      <div class="helper-text">
        {aliasCount} superseded alias node{aliasCount === 1 ? '' : 's'} are folded into this lineage.
      </div>
    {/if}
  </div>
{/if}

<style>
  .lineage-panel,
  .lineage-inline {
    display: grid;
    gap: 10px;
  }

  .lineage-panel {
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-panel-muted);
  }

  .lineage-inline {
    padding-top: 2px;
  }

  .lineage-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  .lineage-title {
    display: grid;
    gap: 2px;
    font-weight: 700;
  }

  .lineage-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }

  .lineage-path {
    display: flex;
    gap: 8px;
    align-items: center;
    overflow-x: auto;
    padding-bottom: 2px;
  }

  .lineage-node {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-width: max-content;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--text);
    background: var(--surface);
    text-decoration: none;
  }

  .lineage-node.is-active {
    border-color: var(--brand-strong);
    text-decoration: underline;
  }

  .lineage-node.is-head {
    font-weight: 600;
  }

  .lineage-node-index {
    display: inline-grid;
    place-items: center;
    width: 18px;
    height: 18px;
    color: var(--text-secondary);
    font-size: 0.72rem;
    font-weight: 700;
  }

  .breakable {
    overflow-wrap: anywhere;
  }

  @media (max-width: 720px) {
    .lineage-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
