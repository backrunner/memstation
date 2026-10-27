<script lang="ts">
  export let matchedKeywords: string[] = [];
  export let sourceKeywords: string[] = [];
  export let sourceLabel = 'Summary keyword index';
</script>

{#if matchedKeywords.length > 0 || sourceKeywords.length > 0}
  <div class="keyword-signals">
    {#if matchedKeywords.length > 0}
      <p><span>Matched keywords</span> {matchedKeywords.join(', ')}</p>
    {/if}

    {#if sourceKeywords.length > 0}
      <p><span>Keyword source: {sourceLabel}</span> {sourceKeywords.join(', ')}</p>
    {/if}
  </div>
{/if}

<style>
  .keyword-signals { display: grid; gap: 4px; margin-top: 12px; font-size: 12px; color: var(--text-secondary); }
  p { margin: 0; overflow-wrap: anywhere; }
  span { color: var(--text-muted); margin-right: 8px; }
</style>
