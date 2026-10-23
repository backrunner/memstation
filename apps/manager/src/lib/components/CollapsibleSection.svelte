<script lang="ts">
  import { Collapsible } from 'bits-ui';
  import { ChevronDown } from '@lucide/svelte';
  import type { Snippet } from 'svelte';

  let {
    title,
    children,
    open = $bindable(false),
    disabled = false,
    variant = 'plain',
    class: className = '',
    contentClass = '',
    triggerContent,
    header
  }: {
    title: string;
    children: Snippet;
    open?: boolean;
    disabled?: boolean;
    variant?: 'plain' | 'panel' | 'toolbar';
    class?: string;
    contentClass?: string;
    triggerContent?: Snippet;
    header?: Snippet<[Snippet]>;
  } = $props();
</script>

{#snippet trigger()}
  <Collapsible.Trigger class={variant === 'toolbar' ? 'btn-secondary disclosure-toggle' : 'disclosure-trigger'}>
    {#if triggerContent}{@render triggerContent()}{:else}<span>{title}</span>{/if}
    <ChevronDown class="disclosure-chevron" size={14} strokeWidth={1.75} aria-hidden="true" />
  </Collapsible.Trigger>
{/snippet}

<Collapsible.Root bind:open {disabled} class={`disclosure disclosure-${variant} ${className}`.trim()}>
  {#if header}{@render header(trigger)}{:else}{@render trigger()}{/if}
  <Collapsible.Content class="disclosure-content" inert={!open}>
    <div class={`disclosure-body ${contentClass}`.trim()}>{@render children()}</div>
  </Collapsible.Content>
</Collapsible.Root>
