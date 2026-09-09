<script lang="ts">
  import { Label, ToggleGroup } from 'bits-ui';

  const id = $props.id();

  interface Props {
    label?: string;
    value?: string;
    items?: Array<{ value: string; label: string }>;
    help?: string;
    error?: string;
    disabled?: boolean;
    fieldClass?: string;
  }

  let {
    label = '',
    value = $bindable(''),
    items = [],
    help = '',
    error = '',
    disabled = false,
    fieldClass = ''
  }: Props = $props();
</script>

<div class={`field ${fieldClass}`.trim()}>
  <Label.Root class="field-label" id={`${id}-label`}>{label}</Label.Root>
  <ToggleGroup.Root
    class={`segmented-control ${error ? 'is-invalid' : ''}`.trim()}
    type="single"
    bind:value
    {disabled}
    aria-invalid={Boolean(error)}
    aria-describedby={error ? `${id}-error` : help ? `${id}-help` : undefined}
    aria-labelledby={`${id}-label`}
  >
    {#each items as item}
      <ToggleGroup.Item class="segmented-control-item" value={item.value}>
        {item.label}
      </ToggleGroup.Item>
    {/each}
  </ToggleGroup.Root>
  {#if help}
    <div class="field-help" id={`${id}-help`}>{help}</div>
  {/if}
  {#if error}
    <div class="field-error" id={`${id}-error`} role="alert">{error}</div>
  {/if}
</div>
