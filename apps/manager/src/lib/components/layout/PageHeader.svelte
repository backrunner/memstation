<script lang="ts">
  export let title = '';
  export let subtitle = '';
  export let description = '';
  export let eyebrow = '';
  export let sticky = false;

  // Publish the actual height so section navigation and anchors also work
  // when actions, status text or accessibility font sizes wrap.
  function measureStickyHeader(node: HTMLElement, enabled: boolean) {
    let cleanup = () => {};
    function update(active: boolean) {
      cleanup();
      cleanup = () => {};
      const container = node.parentElement;
      if (!active || !container) return;
      const previous = container.style.getPropertyValue('--page-header-height');
      const measure = () => container.style.setProperty('--page-header-height', `${node.getBoundingClientRect().height}px`);
      const observer = new ResizeObserver(measure);
      observer.observe(node);
      measure();
      cleanup = () => {
        observer.disconnect();
        if (previous) container.style.setProperty('--page-header-height', previous);
        else container.style.removeProperty('--page-header-height');
      };
    }
    update(enabled);
    return { update, destroy: () => cleanup() };
  }
</script>

<header use:measureStickyHeader={sticky} class="page-header" class:is-sticky={sticky} data-has-context={eyebrow || description ? 'true' : undefined}>
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
