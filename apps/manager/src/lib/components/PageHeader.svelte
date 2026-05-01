<script lang="ts">
  export let title = '';
  export let subtitle = '';
  export let description = '';
  export let eyebrow = '';
</script>

<header class="page-header">
  <div class="page-header-meta">
    {#if eyebrow}
      <span class="sidebar-label">{eyebrow}</span>
    {/if}
    <h1 class="page-title">{title}</h1>
    {#if subtitle}
      <p class="panel-meta">{subtitle}</p>
    {/if}
    {#if description}
      <p class="page-description">{description}</p>
    {/if}
  </div>
  <div class="page-header-actions">
    <slot name="actions" />
  </div>
</header>
