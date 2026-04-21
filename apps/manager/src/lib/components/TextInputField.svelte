<svelte:options runes={false} />

<script lang="ts">
  import { Label } from 'bits-ui';
  import type { HTMLInputAttributes } from 'svelte/elements';

  export let label = '';
  export let value: string | number = '';
  export let type: HTMLInputAttributes['type'] = 'text';
  export let placeholder = '';
  export let help = '';
  export let error = '';
  export let autocomplete: HTMLInputAttributes['autocomplete'] = undefined;
  export let inputmode: HTMLInputAttributes['inputmode'] = undefined;
  export let min: HTMLInputAttributes['min'] = undefined;
  export let max: HTMLInputAttributes['max'] = undefined;
  export let step: HTMLInputAttributes['step'] = undefined;
  export let name: HTMLInputAttributes['name'] = undefined;
  export let disabled = false;
  export let readonly = false;
  export let required = false;
  export let spellcheck = false;
  export let fieldClass = '';
  export let inputClass = '';
  export let inputId = `input-${Math.random().toString(36).slice(2, 9)}`;
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" for={inputId}>{label}</Label.Root>
  <input
    id={inputId}
    class={`input ${inputClass} ${error ? 'is-invalid' : ''}`.trim()}
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
    <div class="field-help">{help}</div>
  {/if}
  {#if error}
    <div class="field-error">{error}</div>
  {/if}
</div>
