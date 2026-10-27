<script lang="ts">
  import { Checkbox, Label } from 'bits-ui';
  import { Check } from '@lucide/svelte';
  const id = $props.id();
  let { label, checked = $bindable(false), disabled = false, onCheckedChange }: {
    label: string; checked?: boolean; disabled?: boolean; onCheckedChange?: (checked: boolean) => void;
  } = $props();
</script>

<Label.Root for={id} class="checkbox-field">
  <Checkbox.Root {id} bind:checked {disabled} {onCheckedChange} class="checkbox-control">
    {#if checked}<Check size={13} strokeWidth={2} />{/if}
  </Checkbox.Root>
  <span>{label}</span>
</Label.Root>

<style>
  :global(.checkbox-field) { display: flex; align-items: center; gap: 10px; min-height: 36px; font-size: var(--font-size-sm); cursor: pointer; }
  :global(.checkbox-control) { display: grid; place-items: center; width: 17px; height: 17px; flex: 0 0 17px; border: 1px solid var(--border-strong); border-radius: 3px; background: var(--surface); color: var(--surface); padding: 0; }
  :global(.checkbox-control[data-state='checked']) { background: var(--brand); border-color: var(--brand); }
  :global(.checkbox-control:focus-visible) { outline: 2px solid var(--brand); outline-offset: 3px; }
  :global(.checkbox-control:disabled) { opacity: .5; cursor: not-allowed; }
  @media (max-width: 600px) { :global(.checkbox-field) { min-height: 44px; } }
</style>
