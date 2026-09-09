<script lang="ts">
  import { Label, Select } from 'bits-ui';
  import { ChevronDown } from 'lucide-svelte';

  const id = $props.id();

  interface Props {
    label?: string;
    value?: string;
    items?: Array<{ value: string; label: string }>;
    placeholder?: string;
    help?: string;
    error?: string;
    disabled?: boolean;
    fieldClass?: string;
    triggerClass?: string;
  }

  let {
    label = '',
    value = $bindable(''),
    items = [],
    placeholder = 'Select',
    help = '',
    error = '',
    disabled = false,
    fieldClass = '',
    triggerClass = ''
  }: Props = $props();
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" id={`${id}-label`} for={id}>{label}</Label.Root>
  <Select.Root type="single" bind:value {items} {disabled}>
    <Select.Trigger id={id} aria-invalid={Boolean(error)} aria-describedby={error ? `${id}-error` : help ? `${id}-help` : undefined} class={`select-trigger ${triggerClass} ${error ? 'is-invalid' : ''}`.trim()} aria-labelledby={`${id}-label`}>
      <span class="select-value">{items.find((item) => item.value === value)?.label ?? placeholder}</span>
      <ChevronDown class="select-chevron" size={14} strokeWidth={1.75} aria-hidden="true" />
    </Select.Trigger>
    <Select.Portal>
      <Select.Content class="select-content" sideOffset={8}>
        <Select.Viewport class="select-viewport">
          {#each items as item}
            <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
          {/each}
        </Select.Viewport>
      </Select.Content>
    </Select.Portal>
  </Select.Root>
  {#if help}
    <div class="field-help" id={`${id}-help`}>{help}</div>
  {/if}
  {#if error}
    <div class="field-error" id={`${id}-error`} role="alert">{error}</div>
  {/if}
</div>
