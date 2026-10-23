<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { highlightCodeToHtml } from '#lib/highlight/shiki.ts';

  export let code = '';
  export let language = 'text';

  let mounted = false;
  let highlightedHtml = '';

  onMount(() => {
    mounted = true;
    return () => { requestToken += 1; };
  });
  let requestToken = 0;

  async function refreshHighlight(): Promise<void> {
    if (!mounted) {
      return;
    }

    const currentToken = ++requestToken;
    try {
      const nextHtml = await highlightCodeToHtml(code, language);
      if (currentToken !== requestToken) {
        return;
      }
      highlightedHtml = nextHtml;
    } catch {
      if (currentToken !== requestToken) {
        return;
      }
      highlightedHtml = '';
    }
  }

  $: highlightKey = `${language}\u0000${code}`;
  $: if (mounted && highlightKey) {
    void refreshHighlight();
  }

</script>

<div class="code-block-host">
  {#if highlightedHtml}
    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
    {@html highlightedHtml}
  {:else}
    <pre class="code-block-surface code-block-fallback" data-language={language}><code>{code}</code></pre>
  {/if}
</div>
