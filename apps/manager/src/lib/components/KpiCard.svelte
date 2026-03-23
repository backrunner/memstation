<script lang="ts">
  export let label = '';
  export let value = '';
  export let meta = '';
</script>

<article class="kpi-card">
  <span class="kpi-label">{label}</span>
  <strong class="kpi-value">{value}</strong>
  {#if meta}
    <span class="kpi-meta">{meta}</span>
  {/if}
</article>
