<svelte:options runes={false} />

<script lang="ts">
  import { Label } from 'bits-ui';

  export let label = '';
  export let value = '';
  export let placeholder = '';
  export let help = '';
  export let error = '';
  export let disabled = false;
  export let readonly = false;
  export let required = false;
  export let spellcheck = false;
  export let rows = 5;
  export let fieldClass = '';
  export let textareaClass = '';
  export let textareaId = `textarea-${Math.random().toString(36).slice(2, 9)}`;
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" for={textareaId}>{label}</Label.Root>
  <textarea
    id={textareaId}
    class={`textarea ${textareaClass} ${error ? 'is-invalid' : ''}`.trim()}
    bind:value
    {placeholder}
    {disabled}
    {readonly}
    {required}
    {spellcheck}
    {rows}
  ></textarea>
  {#if help}
    <div class="field-help">{help}</div>
  {/if}
  {#if error}
    <div class="field-error">{error}</div>
  {/if}
</div>
