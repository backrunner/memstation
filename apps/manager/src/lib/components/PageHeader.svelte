<script lang="ts">
  export let title = '';
  export let subtitle = '';
  export let description = '';
  export let eyebrow = '';
</script>

<header class="page-header" data-has-context={eyebrow || description ? 'true' : undefined}>
  <div class="page-header-meta">
    <h1 class="page-title">{title}</h1>
    {#if subtitle}
      <p class="page-context">{subtitle}</p>
    {/if}
  </div>
  <div class="page-header-actions">
    <slot name="actions" />
  </div>
</header>
