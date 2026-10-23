<script lang="ts">
  import { Label } from 'bits-ui';
  import type { HTMLTextareaAttributes } from 'svelte/elements';

  const id = $props.id();

  interface Props extends Omit<HTMLTextareaAttributes, 'value' | 'class' | 'id'> {
    label: string;
    hideLabel?: boolean;
    value?: string;
    placeholder?: string;
    help?: string;
    error?: string;
    disabled?: boolean;
    readonly?: boolean;
    required?: boolean;
    spellcheck?: boolean;
    rows?: number;
    fieldClass?: string;
    textareaClass?: string;
    textareaId?: string;
  }

  let {
    label,
    hideLabel = false,
    value = $bindable(''),
    placeholder = '',
    help = '',
    error = '',
    disabled = false,
    readonly = false,
    required = false,
    spellcheck = false,
    rows = 5,
    fieldClass = '',
    textareaClass = '',
    textareaId = id,
    ...restProps
  }: Props = $props();
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class={hideLabel ? 'sr-only' : 'field-label'} for={textareaId}>{label}</Label.Root>
  <textarea
    {...restProps}
    id={textareaId}
    class={`textarea ${textareaClass} ${error ? 'is-invalid' : ''}`.trim()}
    aria-invalid={Boolean(error)}
    aria-describedby={[help && `${id}-help`, error && `${id}-error`].filter(Boolean).join(' ') || undefined}
    bind:value
    {placeholder}
    {disabled}
    {readonly}
    {required}
    {spellcheck}
    {rows}
  ></textarea>
  {#if help}
    <div class="field-help" id={`${id}-help`}>{help}</div>
  {/if}
  {#if error}
    <div class="field-error" id={`${id}-error`} role="alert">{error}</div>
  {/if}
</div>
