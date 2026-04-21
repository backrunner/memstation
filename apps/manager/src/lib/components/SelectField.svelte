<svelte:options runes={false} />

<script lang="ts">
  import { Label, Select } from 'bits-ui';

  export let label = '';
  export let value = '';
  export let items: Array<{ value: string; label: string }> = [];
  export let placeholder = 'Select';
  export let help = '';
  export let error = '';
  export let disabled = false;
  export let fieldClass = '';
  export let triggerClass = '';

  const labelId = `select-label-${Math.random().toString(36).slice(2, 9)}`;
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" id={labelId}>{label}</Label.Root>
  <Select.Root type="single" bind:value {items} {disabled}>
    <Select.Trigger class={`select-trigger ${triggerClass} ${error ? 'is-invalid' : ''}`.trim()} aria-labelledby={labelId}>
      {items.find((item) => item.value === value)?.label ?? placeholder}
    </Select.Trigger>
    <Select.Portal>
      <Select.Content class="select-content" sideOffset={8}>
        <Select.Viewport>
          {#each items as item}
            <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
          {/each}
        </Select.Viewport>
      </Select.Content>
    </Select.Portal>
  </Select.Root>
  {#if help}
    <div class="field-help">{help}</div>
  {/if}
  {#if error}
    <div class="field-error">{error}</div>
  {/if}
</div>
