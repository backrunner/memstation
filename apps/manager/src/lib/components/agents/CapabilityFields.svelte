<svelte:options runes={false} />
<script lang="ts">
  import { capabilityOptions } from './capabilities.ts';
  export let value: string[] = [];
  export let allowed = capabilityOptions.map((item) => item.value);
  export let disabled = false;
</script>

<div class="capabilities">
  {#each capabilityOptions as item}
    <label class:unavailable={!allowed.includes(item.value)}>
      <input type="checkbox" bind:group={value} value={item.value} disabled={disabled || !allowed.includes(item.value)} />
      <span><strong>{item.label}</strong><span class="helper-text">{item.help}{!allowed.includes(item.value) ? ' Not currently granted.' : ''}</span></span>
    </label>
  {/each}
</div>

<style>
  .capabilities { display: grid; gap: 4px; }
  label { display: flex; align-items: center; gap: 12px; min-height: 48px; padding: 6px 0; cursor: pointer; }
  input { width: 18px; height: 18px; flex: 0 0 18px; accent-color: var(--brand); }
  strong { font-size: var(--font-size-sm); font-weight: 500; }
  .helper-text { display: block; margin-top: 2px; }
  .unavailable { color: var(--text-muted); cursor: default; }
</style>
