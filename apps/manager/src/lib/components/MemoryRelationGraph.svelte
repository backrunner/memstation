<svelte:options runes={false} />

<script lang="ts">
  import { memoryDetailHref, memoryRouteKindFromDocumentType } from '$lib/memory/routes';
  import type { RelatedMemory } from '$lib/types';

  export let centerLabel = 'Memory';
  export let centerType = 'memory';
  export let items: RelatedMemory[] = [];
  export let userId: string | undefined = undefined;
</script>

{#if items.length === 0}
  <p class="helper-text">No related memories.</p>
{:else}
  <div class="relation-map" aria-label="Memory relations">
    <div class="relation-origin">
      <span class="helper-text">{centerType}</span>
      <strong class="mono">{centerLabel}</strong>
    </div>
    <ul class="relation-branches">
      {#each items as relation}
        {@const kind = memoryRouteKindFromDocumentType(relation.document_type ?? '')}
        {@const id = relation.document_id ?? relation.node_id ?? ''}
        <li class="relation-branch">
          <div class="relation-edge">
            <span>{relation.relation_type ?? 'related'}</span>
            <span class="mono">weight {relation.weight.toFixed(2)}</span>
          </div>
          <div class="relation-node">
            <span class="helper-text">{relation.document_type ?? 'Memory node'}</span>
            {#if kind && relation.document_id}
              <a class="relation-node-link mono" tabindex="0" href={memoryDetailHref(kind, relation.document_id, userId)}>{id}</a>
            {:else}
              <strong class="mono">{id}</strong>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  </div>
{/if}

<style>
  .relation-map { display: grid; gap: 4px; min-width: 0; }
  .relation-origin { display: grid; gap: 2px; padding: 12px 14px; border: 1px solid var(--border-strong); border-radius: var(--radius-sm); background: var(--surface); overflow-wrap: anywhere; }
  .relation-branches { list-style: none; padding: 0 0 0 18px; margin: 0; }
  .relation-branch { position: relative; padding: 14px 0 0 20px; border-left: 1px solid var(--border-strong); }
  .relation-branch::before { content: ''; position: absolute; left: 0; top: 27px; width: 12px; border-top: 1px solid var(--border-strong); }
  .relation-edge { display: flex; justify-content: space-between; flex-wrap: wrap; gap: 4px 12px; margin-bottom: 8px; color: var(--text-muted); font-size: var(--font-size-xs); }
  .relation-node { display: grid; gap: 2px; padding: 10px 12px; background: var(--surface-muted); border-radius: var(--radius-sm); overflow-wrap: anywhere; }
  .relation-node-link { color: var(--brand); font-weight: 500; width: fit-content; max-width: 100%; text-decoration: underline; text-underline-offset: 3px; }
  .relation-node-link:hover { color: var(--brand-strong); }
  @media (pointer: coarse) { .relation-node-link { display: flex; align-items: center; min-height: 44px; } }
</style>
