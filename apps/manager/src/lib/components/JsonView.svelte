<svelte:options runes={false} />

<script lang="ts">
  import CodeBlock from '#lib/components/CodeBlock.svelte';

  export let value: unknown = {};
  export let emptyLabel = 'No data';

  $: isEmptyValue =
    value == null || (typeof value === 'object' && !Array.isArray(value) && Object.keys(value as Record<string, unknown>).length === 0);
  $: serialized = isEmptyValue ? emptyLabel : JSON.stringify(value, null, 2);
</script>

<CodeBlock code={serialized} language={isEmptyValue ? 'text' : 'json'} />
