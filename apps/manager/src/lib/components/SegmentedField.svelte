<svelte:options runes={false} />

<script lang="ts">
  import { Label, ToggleGroup } from 'bits-ui';

  export let label = '';
  export let value = '';
  export let items: Array<{ value: string; label: string }> = [];
  export let help = '';
  export let error = '';
  export let disabled = false;
  export let fieldClass = '';

  const labelId = `segment-label-${Math.random().toString(36).slice(2, 9)}`;
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" id={labelId}>{label}</Label.Root>
  <ToggleGroup.Root
    class={`segmented-control ${error ? 'is-invalid' : ''}`.trim()}
    type="single"
    bind:value
    {disabled}
    aria-labelledby={labelId}
  >
    {#each items as item}
      <ToggleGroup.Item class="segmented-control-item" value={item.value}>
        {item.label}
      </ToggleGroup.Item>
    {/each}
  </ToggleGroup.Root>
  {#if help}
    <div class="field-help">{help}</div>
  {/if}
  {#if error}
    <div class="field-error">{error}</div>
  {/if}
</div>
