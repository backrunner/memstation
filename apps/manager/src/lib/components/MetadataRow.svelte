<script lang="ts">
  let { items, class: className = '' }: { items: (string | number | null | undefined | false)[]; class?: string } = $props();
  let entries = $derived(items.filter(item => item !== null && item !== undefined && item !== false && item !== ''));
</script>

<span class={`metadata-row ${className}`.trim()}>
  <!-- Keep whitespace in copied text and accessible names; CSS supplies the visual spacing. -->
  {#each entries as item, index}<span>{item}{index < entries.length - 1 ? ' ' : ''}</span>{/each}
</span>

<style>
  .metadata-row { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 16px; color: var(--text-muted); font-size: var(--font-size-sm); line-height: 1.5; }
  .metadata-row > span { min-width: 0; overflow-wrap: anywhere; }
</style>
