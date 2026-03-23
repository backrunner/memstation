<svelte:options runes={false} />

<script lang="ts">
  export let centerLabel = 'Memory';
  export let centerType = 'memory';
  export let items: Array<{ id: string; type: string; weight: number }> = [];

  const size = 360;
  const center = { x: size / 2, y: size / 2 };
  const orbit = 112;

  $: visibleItems = items.slice(0, 6);
  $: nodes = visibleItems.map((item, index) => {
    const angle = (-Math.PI / 2) + ((Math.PI * 2) / Math.max(visibleItems.length, 1)) * index;
    return {
      ...item,
      x: center.x + Math.cos(angle) * orbit,
      y: center.y + Math.sin(angle) * orbit
    };
  });

  function shortLabel(value: string): string {
    return value.length > 18 ? `${value.slice(0, 16)}..` : value;
  }

  function tone(type: string): string {
    switch (type) {
      case 'summary':
        return 'var(--brand-strong)';
      case 'fact':
        return 'var(--success)';
      case 'impression':
        return 'var(--warning)';
      case 'key_memory':
        return 'var(--danger)';
      default:
        return 'var(--text-secondary)';
    }
  }
</script>

{#if visibleItems.length === 0}
  <div class="empty-state">No related nodes are available to graph.</div>
{:else}
  <div class="memory-graph-shell">
    <svg viewBox={`0 0 ${size} ${size}`} class="memory-graph" role="img" aria-label="memory relation graph">
      <defs>
        <radialGradient id="memoryGraphGlow" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stop-color="rgba(15, 94, 168, 0.22)" />
          <stop offset="100%" stop-color="rgba(15, 94, 168, 0)" />
        </radialGradient>
      </defs>

      <circle cx={center.x} cy={center.y} r="154" fill="url(#memoryGraphGlow)" />

      {#each nodes as node}
        <line
          x1={center.x}
          y1={center.y}
          x2={node.x}
          y2={node.y}
          stroke={tone(node.type)}
          stroke-opacity={Math.max(0.3, Math.min(0.9, node.weight))}
          stroke-width={2 + Math.max(0, node.weight * 2)}
        />
      {/each}

      <circle cx={center.x} cy={center.y} r="42" fill="rgba(15, 94, 168, 0.12)" stroke="var(--brand-strong)" stroke-width="2.5" />
      <text x={center.x} y={center.y - 2} text-anchor="middle" class="memory-graph-title">{shortLabel(centerLabel)}</text>
      <text x={center.x} y={center.y + 18} text-anchor="middle" class="memory-graph-subtitle">{centerType}</text>

      {#each nodes as node}
        <circle cx={node.x} cy={node.y} r="24" fill="white" stroke={tone(node.type)} stroke-width="2.5" />
        <text x={node.x} y={node.y - 2} text-anchor="middle" class="memory-graph-title">{shortLabel(node.id)}</text>
        <text x={node.x} y={node.y + 16} text-anchor="middle" class="memory-graph-subtitle">{node.type}</text>
      {/each}
    </svg>
  </div>
{/if}
