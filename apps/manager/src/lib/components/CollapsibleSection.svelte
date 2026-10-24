<script lang="ts">
  import { Collapsible } from 'bits-ui';
  import { ChevronDown } from '@lucide/svelte';
  import { tick, type Snippet } from 'svelte';

  const id = $props.id();
  let {
    title,
    children,
    open = $bindable(false),
    disabled = false,
    count,
    description = '',
    icon,
    variant = 'plain',
    class: className = '',
    contentClass = '',
    header
  }: {
    title: string;
    children: Snippet;
    open?: boolean;
    disabled?: boolean;
    count?: number;
    description?: string;
    icon?: Snippet;
    variant?: 'plain' | 'panel' | 'toolbar';
    class?: string;
    contentClass?: string;
    header?: Snippet<[Snippet]>;
  } = $props();
  const label = $derived(`${title}${count === undefined ? '' : ` (${count})`}`);

  // Contents stay mounted when closed: native GET forms must still submit all
  // fields and local editor state must survive toggles. Reveal invalid fields
  // before the browser tries to focus a control inside the closed region.
  async function revealInvalidField(event: Event): Promise<void> {
    if (open || disabled || !(event.target instanceof HTMLElement)) return;
    const field = event.target;
    event.preventDefault();
    open = true;
    await tick();
    if (!field.isConnected) return;
    field.focus();
    if (field instanceof HTMLInputElement || field instanceof HTMLSelectElement || field instanceof HTMLTextAreaElement) field.reportValidity();
  }
</script>

{#snippet trigger()}
  <Collapsible.Trigger
    class="disclosure-trigger"
    aria-label={label}
    aria-describedby={description ? `${id}-description` : undefined}
  >
    {#if icon}<span class="disclosure-icon" aria-hidden="true">{@render icon()}</span>{/if}
    <span class="disclosure-heading">
      <span class="disclosure-title">{title}{#if count !== undefined}<span class="disclosure-count">({count})</span>{/if}</span>
      {#if description}<span class="disclosure-description" id={`${id}-description`}>{description}</span>{/if}
    </span>
    <span class="disclosure-affordance" aria-hidden="true"><ChevronDown class="disclosure-chevron" size={16} strokeWidth={1.75} /></span>
  </Collapsible.Trigger>
{/snippet}

<Collapsible.Root bind:open {disabled} class={`disclosure disclosure-${variant} ${className}`.trim()}>
  {#if header}{@render header(trigger)}{:else}{@render trigger()}{/if}
  <Collapsible.Content class="disclosure-content" inert={!open} oninvalidcapture={revealInvalidField}>
    <div class={`disclosure-body ${contentClass}`.trim()}>{@render children()}</div>
  </Collapsible.Content>
</Collapsible.Root>

<style>
  :global(.disclosure) { min-width: 0; }
  :global(.disclosure-plain) { border-block: 1px solid var(--border-subtle); }
  :global(.disclosure-panel) { border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface); }
  :global(.disclosure-trigger) {
    display: flex; align-items: center; gap: 10px; width: 100%; min-height: 44px;
    padding: 10px 12px; border: 0; border-radius: var(--radius-sm);
    background: transparent; color: var(--text-secondary); font: inherit; font-size: var(--font-size-sm);
    font-weight: 500; text-align: left; cursor: pointer;
    transition: background-color 140ms ease, border-color 140ms ease, color 140ms ease;
  }
  :global(.disclosure-trigger:hover:not(:disabled)) { color: var(--text); background: var(--surface-hover); }
  :global(.disclosure-trigger[data-state='open']) { color: var(--brand-strong); background: var(--brand-softer); }
  :global(.disclosure-trigger:focus-visible) { outline: 2px solid var(--brand); outline-offset: 3px; }
  :global(.disclosure-trigger:disabled) { opacity: 0.5; cursor: not-allowed; }
  :global(.disclosure-heading) { display: grid; gap: 2px; flex: 1; min-width: 0; }
  :global(.disclosure-title) { display: flex; align-items: baseline; flex-wrap: wrap; column-gap: 6px; font-weight: 600; overflow-wrap: anywhere; }
  :global(.disclosure-count) { font-weight: 500; color: var(--text-muted); font-variant-numeric: tabular-nums; }
  :global(.disclosure-description) { color: var(--text-muted); font-weight: 400; font-size: var(--font-size-xs); overflow-wrap: anywhere; }
  :global(.disclosure-icon) { display: inline-flex; align-items: center; flex-shrink: 0; color: var(--text-muted); }
  :global(.disclosure-affordance) { display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 24px; height: 24px; border-radius: 4px; background: var(--surface-muted); color: var(--text-muted); }
  :global(.disclosure-chevron) { transition: transform 160ms ease; }
  :global(.disclosure-trigger[data-state='open'] .disclosure-affordance) { background: var(--brand-soft); color: var(--brand); }
  :global(.disclosure-trigger[data-state='open'] .disclosure-chevron) { transform: rotate(180deg); }
  :global(.disclosure-trigger[data-state='open'] .disclosure-icon), :global(.disclosure-trigger[data-state='open'] .disclosure-count) { color: var(--brand); }
  :global(.disclosure-plain > .disclosure-trigger) { border-radius: 0; }
  :global(.disclosure-panel > .disclosure-trigger) { padding-inline: 16px; }
  :global(.disclosure-toolbar .disclosure-trigger) {
    width: auto; min-height: var(--control-height); padding: 5px 10px;
    border: 1px solid var(--border); background: var(--surface); gap: 8px;
  }
  :global(.disclosure-toolbar .disclosure-trigger:hover:not(:disabled)) { border-color: var(--border-strong); background: var(--surface-hover); }
  :global(.disclosure-toolbar .disclosure-trigger[data-state='open']) { border-color: var(--brand); background: var(--brand-softer); }
  :global(.disclosure-toolbar .disclosure-affordance) { width: 18px; height: 18px; background: transparent; }
  :global(.disclosure-content) { min-width: 0; overflow: hidden; }
  :global(.disclosure-content[data-state='open']) { animation: disclosure-open 160ms ease-out; }
  :global(.disclosure-content[data-state='closed']) { animation: disclosure-close 130ms ease-in; }
  :global(.disclosure-content[hidden]) { display: none; }
  :global(.disclosure-body) { display: grid; gap: 12px; min-width: 0; padding: 12px 12px 16px; }
  :global(.disclosure-panel > .disclosure-content > .disclosure-body) { padding-inline: 16px; }
  :global(.disclosure-toolbar > .disclosure-content > .disclosure-body) { margin-top: 12px; padding: 16px 0 4px; border-top: 1px solid var(--border-subtle); }
  @keyframes -global-disclosure-open { from { height: 0; } to { height: var(--bits-collapsible-content-height); } }
  @keyframes -global-disclosure-close { from { height: var(--bits-collapsible-content-height); } to { height: 0; } }
  @media (prefers-reduced-motion: reduce) {
    :global(.disclosure-content) { animation: none !important; }
    :global(.disclosure-chevron), :global(.disclosure-trigger) { transition: none; }
  }
</style>
