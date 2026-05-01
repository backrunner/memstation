<script lang="ts">
  export let label = '';
  export let value = '';
  export let meta = '';
  export let href = '';
  export let trend: 'positive' | 'negative' | undefined = undefined;
  export let trendLabel = '';
</script>

{#if href}
  <a class="kpi-card is-clickable" {href}>
    <span class="kpi-label">{label}</span>
    <strong class="kpi-value">{value}</strong>
    {#if meta || trendLabel}
      <div class="kpi-meta">
        {#if trend && trendLabel}
          <span class={`kpi-trend ${trend}`}>{trendLabel}</span>
          {#if meta}<span> · {meta}</span>{/if}
        {:else if meta}
          <span>{meta}</span>
        {/if}
      </div>
    {/if}
  </a>
{:else}
  <article class="kpi-card">
    <span class="kpi-label">{label}</span>
    <strong class="kpi-value">{value}</strong>
    {#if meta || trendLabel}
      <div class="kpi-meta">
        {#if trend && trendLabel}
          <span class={`kpi-trend ${trend}`}>{trendLabel}</span>
          {#if meta}<span> · {meta}</span>{/if}
        {:else if meta}
          <span>{meta}</span>
        {/if}
      </div>
    {/if}
  </article>
{/if}
