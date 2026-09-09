<script lang="ts">
  export let title = '';
  export let subtitle = '';
  export let description = '';
  export let eyebrow = '';
  export let sticky = false;
</script>

<header class="page-header" class:is-sticky={sticky} data-has-context={eyebrow || description ? 'true' : undefined}>
  <div class="page-header-meta">
    <h1 class="page-title">{title}</h1>
    {#if subtitle}
      <p class="page-context">{subtitle}</p>
    {/if}
  </div>
  {#if $$slots.actions}
    <div class="page-header-actions">
      <slot name="actions" />
    </div>
  {/if}
</header>
