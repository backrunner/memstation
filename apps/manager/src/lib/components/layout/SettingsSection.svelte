<script lang="ts">
  import type { Snippet } from 'svelte';

  let { id, title, description = '', children }: {
    id: string;
    title: string;
    description?: string;
    children: Snippet;
  } = $props();
</script>

<section {id} class="settings-section" aria-labelledby={`${id}-title`} tabindex="-1">
  <header class="settings-section-intro">
    <h2 id={`${id}-title`} class="section-title">{title}</h2>
    {#if description}<p class="panel-meta">{description}</p>{/if}
  </header>
  <div class="settings-section-fields">{@render children()}</div>
</section>

<style>
  .settings-section { display: grid; grid-template-columns: minmax(140px, 0.8fr) minmax(0, 2.4fr); gap: 28px; padding: 24px; scroll-margin-top: var(--page-anchor-offset, 150px); }
  .settings-section:not(:first-of-type) { border-top: 1px solid var(--border); }
  .settings-section-intro { display: grid; align-content: start; gap: 6px; }
  .settings-section-fields { display: grid; align-content: start; gap: 16px; min-width: 0; }
  @media (max-width: 1200px) { .settings-section { grid-template-columns: 1fr; gap: 18px; } }
  @media (max-width: 640px) { .settings-section { padding: 20px 16px; } }
</style>
