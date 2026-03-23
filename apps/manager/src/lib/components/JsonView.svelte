<svelte:options runes={false} />

<script lang="ts">
  export let value: unknown = {};
  export let emptyLabel = 'No data';

  $: serialized = value == null || (typeof value === 'object' && !Array.isArray(value) && Object.keys(value as Record<string, unknown>).length === 0)
    ? emptyLabel
    : JSON.stringify(value, null, 2);
</script>

<pre class="json-block">{serialized}</pre>
