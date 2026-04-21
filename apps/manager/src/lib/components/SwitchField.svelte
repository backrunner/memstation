<svelte:options runes={false} />

<script lang="ts">
  import { Label, Switch } from 'bits-ui';

  export let label = '';
  export let help = '';
  export let checked = false;
  export let disabled = false;
  export let error = '';
  export let fieldClass = '';

  const labelId = `switch-label-${Math.random().toString(36).slice(2, 9)}`;
  const helpId = help ? `switch-help-${Math.random().toString(36).slice(2, 9)}` : undefined;
</script>

<div class={`field switch-field ${fieldClass}`.trim()}>
  <div class={`switch-row ${error ? 'is-invalid' : ''}`}>
    <div>
      <Label.Root class="field-label" id={labelId}>{label}</Label.Root>
      {#if help}
        <div class="field-help" id={helpId}>{help}</div>
      {/if}
    </div>
    <Switch.Root
      class="switch-root"
      bind:checked
      {disabled}
      aria-labelledby={labelId}
      aria-describedby={helpId}
    >
      <Switch.Thumb class="switch-thumb" />
    </Switch.Root>
  </div>
  {#if error}
    <div class="field-error">{error}</div>
  {/if}
</div>
