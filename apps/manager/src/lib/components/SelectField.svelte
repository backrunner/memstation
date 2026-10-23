<script lang="ts" generics="T extends { value: string; label: string; disabled?: boolean }">
  import { Label, Select } from 'bits-ui';
  import { Check, ChevronDown } from '@lucide/svelte';
  import type { Snippet } from 'svelte';

  const id = $props.id();
  let {
    label,
    value = $bindable(''),
    items = [],
    placeholder = 'Select',
    help = '',
    error = '',
    disabled = false,
    required = false,
    name,
    hideLabel = false,
    fieldClass = '',
    triggerClass = '',
    contentClass = '',
    itemClass = '',
    align = 'start',
    title,
    onValueChange,
    selection,
    option
  }: {
    label: string;
    value?: string;
    items?: T[];
    placeholder?: string;
    help?: string;
    error?: string;
    disabled?: boolean;
    required?: boolean;
    name?: string;
    hideLabel?: boolean;
    fieldClass?: string;
    triggerClass?: string;
    contentClass?: string;
    itemClass?: string;
    align?: 'start' | 'center' | 'end';
    title?: string;
    onValueChange?: (value: string) => void;
    selection?: Snippet<[T | undefined]>;
    option?: Snippet<[T]>;
  } = $props();
  const selected = $derived(items.find((item) => item.value === value));
  const describedBy = $derived([help && `${id}-help`, error && `${id}-error`].filter(Boolean).join(' ') || undefined);
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class={hideLabel ? 'sr-only' : 'field-label'} id={`${id}-label`} for={id}>{label}</Label.Root>
  <Select.Root type="single" bind:value {items} {disabled} {name} {required} {onValueChange} allowDeselect={false}>
    <Select.Trigger id={id} {title} aria-invalid={Boolean(error)} aria-describedby={describedBy} class={`select-trigger ${triggerClass} ${error ? 'is-invalid' : ''}`.trim()} aria-labelledby={`${id}-label`}>
      {#if selection}{@render selection(selected)}{:else}<span class="select-value">{selected?.label ?? placeholder}</span>{/if}
      <ChevronDown class="select-chevron" size={14} strokeWidth={1.75} aria-hidden="true" />
    </Select.Trigger>
    <Select.Portal>
      <Select.Content class={`select-content ${contentClass}`.trim()} sideOffset={4} collisionPadding={12} {align}>
        <Select.Viewport class="select-viewport">
          {#each items as item (item.value)}
            <Select.Item class={`select-item ${itemClass}`.trim()} value={item.value} label={item.label} disabled={item.disabled}>
              <span class="select-option-label">{#if option}{@render option(item)}{:else}{item.label}{/if}</span>
              <Check class={`select-check ${item.value === value ? '' : 'is-hidden'}`} size={14} strokeWidth={1.75} aria-hidden="true" />
            </Select.Item>
          {/each}
          {#if items.length === 0}<div class="select-empty">No options available</div>{/if}
        </Select.Viewport>
      </Select.Content>
    </Select.Portal>
  </Select.Root>
  {#if help}<div class="field-help" id={`${id}-help`}>{help}</div>{/if}
  {#if error}<div class="field-error" id={`${id}-error`} role="alert">{error}</div>{/if}
</div>
