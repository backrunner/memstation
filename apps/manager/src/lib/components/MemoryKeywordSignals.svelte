<script lang="ts">
  export let matchedKeywords: string[] = [];
  export let sourceKeywords: string[] = [];
  export let sourceLabel = 'Summary keyword index';
</script>

{#if matchedKeywords.length > 0 || sourceKeywords.length > 0}
  <div class="page-stack" style="gap: 10px; margin-top: 12px;">
    {#if matchedKeywords.length > 0}
      <div class="page-stack" style="gap: 6px;">
        <div class="sidebar-label">Matched keywords</div>
        <div class="memory-diff-meta">
          {#each matchedKeywords as keyword}
            <span class="memory-diff-chip">{keyword}</span>
          {/each}
        </div>
      </div>
    {/if}

    {#if sourceKeywords.length > 0}
      <div class="page-stack" style="gap: 6px;">
        <div class="sidebar-label">Keyword source</div>
        <div class="helper-text">{sourceLabel}</div>
        <div class="memory-diff-meta">
          {#each sourceKeywords as keyword}
            <span class="memory-diff-chip">{keyword}</span>
          {/each}
        </div>
      </div>
    {/if}
  </div>
{/if}
