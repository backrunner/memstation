<svelte:options runes={false} />

<script lang="ts">
  import { browser } from '$app/environment';
  import { tick } from 'svelte';

  import { enhanceCodeBlocks } from '$lib/highlight/shiki';
  import { renderMarkdown } from '$lib/utils/markdown';

  export let value = '';
  export let className = 'message-body';

  let container: HTMLDivElement | null = null;
  let enhanceToken = 0;

  async function enhanceRenderedMarkup(): Promise<void> {
    if (!browser || !container) {
      return;
    }

    const currentToken = ++enhanceToken;
    await tick();
    if (currentToken !== enhanceToken || !container) {
      return;
    }
    await enhanceCodeBlocks(container);
  }

  $: html = renderMarkdown(value);
  $: scheduleEnhancement(html, container);

  function scheduleEnhancement(_: string, root: HTMLDivElement | null): void {
    if (browser && root) {
      void enhanceRenderedMarkup();
    }
  }
</script>

<div class={className} bind:this={container}>
  <!-- eslint-disable-next-line svelte/no-at-html-tags -->
  {@html html}
</div>
