<svelte:options runes={false} />

<script lang="ts">
  import { browser } from '$app/environment';

  import { escapeHTML, highlightCodeToHtml } from '$lib/highlight/shiki';

  export let code = '';
  export let language = 'text';

  let highlightedHtml = '';
  let requestToken = 0;

  async function refreshHighlight(): Promise<void> {
    if (!browser) {
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
  $: if (browser && highlightKey) {
    void refreshHighlight();
  }

  $: fallbackHtml = `<pre class="code-block-surface code-block-fallback" data-language="${escapeHTML(language)}"><code>${escapeHTML(code)}</code></pre>`;
</script>

<div class="code-block-host">
  <!-- eslint-disable-next-line svelte/no-at-html-tags -->
  {@html highlightedHtml || fallbackHtml}
</div>
