<svelte:options runes={false} />

<script lang="ts">
  import { Collapsible } from 'bits-ui';

  import JsonView from '$lib/components/JsonView.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { renderMarkdown } from '$lib/utils/markdown';
  import type { ChatThreadMessage } from '$lib/types';

  export let message: ChatThreadMessage;

  let html = '';
  let roleTone: 'warning' | 'brand' | 'success' | 'danger' | 'muted' = 'muted';

  $: html = renderMarkdown(message.text);
  $: roleTone = message.role === 'assistant' ? 'brand' : message.role === 'user' ? 'success' : message.role === 'tool' ? 'warning' : 'muted';
</script>

<article class={`message-card ${message.role}`}>
  <div class="message-meta">
    <StatusBadge tone={roleTone} label={message.role.toUpperCase()} />
    {#if message.usage?.total_tokens}
      <span class="subtle-text">{message.usage.total_tokens} total tokens</span>
    {/if}
    {#if message.pending}
      <StatusBadge tone="warning" label="Streaming" />
    {/if}
    {#if message.error}
      <StatusBadge tone="danger" label="Error" />
    {/if}
  </div>

  {#if message.thinking}
    <Collapsible.Root>
      <Collapsible.Trigger class="collapsible-trigger">Thinking trace</Collapsible.Trigger>
      <Collapsible.Content>
        <div class="debug-block" style="margin-top: 10px;">
          <div class="message-body">{@html renderMarkdown(message.thinking)}</div>
        </div>
      </Collapsible.Content>
    </Collapsible.Root>
  {/if}

  <div class="message-body">{@html html}</div>

  {#if message.toolCalls?.length}
    <Collapsible.Root>
      <Collapsible.Trigger class="collapsible-trigger">Tool calls ({message.toolCalls.length})</Collapsible.Trigger>
      <Collapsible.Content>
        <div class="toolbar" style="margin-top: 10px;">
          {#each message.toolCalls as toolCall}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <strong>{toolCall.name}</strong>
                <span class="subtle-text">{toolCall.id}</span>
              </div>
              <JsonView value={toolCall.arguments ?? {}} />
            </div>
          {/each}
        </div>
      </Collapsible.Content>
    </Collapsible.Root>
  {/if}

  {#if message.toolResults?.length}
    <Collapsible.Root>
      <Collapsible.Trigger class="collapsible-trigger">Tool results ({message.toolResults.length})</Collapsible.Trigger>
      <Collapsible.Content>
        <div class="toolbar" style="margin-top: 10px;">
          {#each message.toolResults as toolResult}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <strong>{toolResult.name ?? 'tool_result'}</strong>
                <span class="subtle-text">{toolResult.tool_call_id}</span>
              </div>
              <JsonView value={toolResult.output ?? {}} />
            </div>
          {/each}
        </div>
      </Collapsible.Content>
    </Collapsible.Root>
  {/if}

  {#if message.metadata && Object.keys(message.metadata).length}
    <div class="message-footer">
      <span class="subtle-text">Metadata available in debug panel.</span>
    </div>
  {/if}
</article>
