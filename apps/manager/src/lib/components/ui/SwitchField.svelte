<script lang="ts">
  import { Label, Switch } from 'bits-ui';

  const id = $props.id();

  interface Props {
    label?: string;
    help?: string;
    checked?: boolean;
    disabled?: boolean;
    error?: string;
    fieldClass?: string;
  }

  let {
    label = '',
    help = '',
    checked = $bindable(false),
    disabled = false,
    error = '',
    fieldClass = ''
  }: Props = $props();
</script>

<div class={`field switch-field ${fieldClass}`.trim()}>
  <div class={`switch-row ${error ? 'is-invalid' : ''}`}>
    <div>
      <Label.Root class="field-label" id={`${id}-label`} for={id}>{label}</Label.Root>
      {#if help}
        <div class="field-help" id={`${id}-help`}>{help}</div>
      {/if}
    </div>
    <Switch.Root
      id={id}
      class="switch-root"
      aria-invalid={Boolean(error)}
      bind:checked
      {disabled}
      aria-labelledby={`${id}-label`}
      aria-describedby={error ? `${id}-error` : help ? `${id}-help` : undefined}
    >
      <Switch.Thumb class="switch-thumb" />
    </Switch.Root>
  </div>
  {#if error}
    <div class="field-error" id={`${id}-error`} role="alert">{error}</div>
  {/if}
</div>
