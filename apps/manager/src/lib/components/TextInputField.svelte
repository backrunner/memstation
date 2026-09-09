<script lang="ts">
  import { Label } from 'bits-ui';
  import type { HTMLInputAttributes } from 'svelte/elements';

  const id = $props.id();

  interface Props {
    label?: string;
    value?: string | number;
    type?: HTMLInputAttributes['type'];
    placeholder?: string;
    help?: string;
    error?: string;
    autocomplete?: HTMLInputAttributes['autocomplete'];
    inputmode?: HTMLInputAttributes['inputmode'];
    min?: HTMLInputAttributes['min'];
    max?: HTMLInputAttributes['max'];
    step?: HTMLInputAttributes['step'];
    name?: HTMLInputAttributes['name'];
    disabled?: boolean;
    readonly?: boolean;
    required?: boolean;
    spellcheck?: boolean;
    fieldClass?: string;
    inputClass?: string;
    inputId?: string;
  }

  let {
    label = '',
    value = $bindable(''),
    type = 'text',
    placeholder = '',
    help = '',
    error = '',
    autocomplete = undefined,
    inputmode = undefined,
    min = undefined,
    max = undefined,
    step = undefined,
    name = undefined,
    disabled = false,
    readonly = false,
    required = false,
    spellcheck = false,
    fieldClass = '',
    inputClass = '',
    inputId = id
  }: Props = $props();
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" for={inputId}>{label}</Label.Root>
  <input
    id={inputId}
    class={`input ${inputClass} ${error ? 'is-invalid' : ''}`.trim()}
    aria-invalid={Boolean(error)}
    aria-describedby={error ? `${id}-error` : help ? `${id}-help` : undefined}
    bind:value
    {type}
    {placeholder}
    {autocomplete}
    {inputmode}
    {min}
    {max}
    {step}
    {name}
    {disabled}
    {readonly}
    {required}
    {spellcheck}
  />
  {#if help}
    <div class="field-help" id={`${id}-help`}>{help}</div>
  {/if}
  {#if error}
    <div class="field-error" id={`${id}-error`} role="alert">{error}</div>
  {/if}
</div>
