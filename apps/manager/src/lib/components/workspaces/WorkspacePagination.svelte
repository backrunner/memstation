<script lang="ts">
  let { path, query, after, next = '', label, invalid = false }: { path: string; query: string; after: string; next?: string; label: string; invalid?: boolean } = $props();
  function href(cursor = '') { const q = new URLSearchParams({ q: query }); if (cursor) q.set('after', cursor); return `${path}?${q}`; }
</script>

{#if after || next || invalid}
  <nav class="pagination" aria-label={`${label} pagination`}>
    {#if after || invalid}<a class="text-link" href={href()}>First page</a>{/if}
    {#if next}<a class="text-link" href={href(next)}>Next {label.toLowerCase()}</a>{/if}
  </nav>
{/if}

<style>
  .pagination { display: flex; flex-wrap: wrap; gap: 12px 24px; }
  a { display: inline-flex; align-items: center; min-height: 44px; }
</style>
