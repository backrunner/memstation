<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { Collapsible, ScrollArea, Select, Switch, Tabs } from 'bits-ui';

  import {
    fetchConversation,
    fetchConversationMemory,
    fetchMemoryPreview,
    requestGatewayExchange
  } from '$lib/api/client';
  import ChatMessageCard from '$lib/components/ChatMessageCard.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import MemoryKeywordSignals from '$lib/components/MemoryKeywordSignals.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { matchedKeywordsFromMetadata, recordList, sourceKeywordsFromMetadata, stringList } from '$lib/memory/keyword-signals';
  import { buildMemoryTurnDiff, type MemoryTurnChange, type MemoryTurnDiffResult, type MemoryTurnKind } from '$lib/playground/memory-diff';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type {
    ChatThreadMessage,
    ConversationMemoryPayload,
    ConversationRecord,
    GatewayDebugState,
    GatewayProtocol,
    MemoryPreviewPayload,
    UsageRecord
  } from '$lib/types';

  const protocolItems = [
    { value: 'chat_completions', label: 'OpenAI Chat Completions' },
    { value: 'responses', label: 'OpenAI Responses' },
    { value: 'anthropic_messages', label: 'Anthropic Messages' }
  ];

  const providerItems = [
    { value: '', label: 'Auto select' },
    { value: 'openai', label: 'OpenAI' },
    { value: 'anthropic', label: 'Anthropic' },
    { value: 'gemini', label: 'Gemini' }
  ];

  let protocol: GatewayProtocol = 'chat_completions';
  let provider = '';
  let mode: 'transparent' | 'inject' = 'inject';
  let sessionId = '';
  let model = '';
  let apiKey = '';
  let injectContext = true;
  let injectTools = false;
  let toolWrite = false;
  let memoryPersist = false;
  let stream = true;
  let input = '';
  let sending = false;
  let error = '';
  let debugTab = 'request';
  let messages: ChatThreadMessage[] = [];
  let debug: GatewayDebugState = { requestHeaders: {}, requestBody: {}, rawEvents: [] };
  let gatewayHeaders = { conversationId: '', requestId: '', memoryPersist: false };
  let conversationDetail: ConversationRecord | null = null;
  let conversationMemory: ConversationMemoryPayload | null = null;
  let memoryPreview: MemoryPreviewPayload | null = null;
  let injectedMemory: Record<string, unknown> | null = null;
  let previousInjectedMemory: Record<string, unknown> | null = null;
  let previousConversationMemory: ConversationMemoryPayload | null = null;
  let previousMemoryPreview: MemoryPreviewPayload | null = null;
  let latestTurnPrompt = '';
  let completedTurns = 0;
  let latestTurnPersistMode = false;
  let debugMemoryPersistMode = false;
  let turnDiff: MemoryTurnDiffResult = { sections: [], changeCount: 0 };

  $: debugMemoryPersistMode = completedTurns > 0 ? latestTurnPersistMode : memoryPersist;
  $: turnDiff = buildMemoryTurnDiff({
    memoryPersist: debugMemoryPersistMode,
    previousInjectedMemory,
    injectedMemory,
    previousConversationMemory,
    conversationMemory,
    previousMemoryPreview,
    memoryPreview
  });

  onMount(() => {
    if (!sessionId) {
      sessionId = `playground-${Date.now()}`;
    }
  });

  function resetThread(): void {
    messages = [];
    input = '';
    error = '';
    conversationDetail = null;
    conversationMemory = null;
    memoryPreview = null;
    injectedMemory = null;
    previousInjectedMemory = null;
    previousConversationMemory = null;
    previousMemoryPreview = null;
    latestTurnPrompt = '';
    completedTurns = 0;
    latestTurnPersistMode = false;
    gatewayHeaders = { conversationId: `playground-${Date.now()}`, requestId: '', memoryPersist: false };
    sessionId = gatewayHeaders.conversationId;
    debug = { requestHeaders: {}, requestBody: {}, rawEvents: [] };
  }

  async function submit(endConversation = false): Promise<void> {
    if (!$selectedTarget) {
      error = 'Select a target before opening the playground.';
      return;
    }
    if (!apiKey.trim()) {
      error = 'Provide a MemStation API key for gateway testing.';
      return;
    }
    if (!model.trim()) {
      error = 'Provide a configured model name before sending.';
      return;
    }
    if (!input.trim()) {
      error = 'Enter a prompt before sending.';
      return;
    }

    sending = true;
    error = '';
    const persistForTurn = memoryPersist;
    previousInjectedMemory = injectedMemory;
    previousConversationMemory = conversationMemory;
    previousMemoryPreview = memoryPreview;
    memoryPreview = null;
    conversationMemory = null;

    const outboundMessages = [
      ...messages,
      {
        id: `user-${Date.now()}`,
        role: 'user' as const,
        text: input.trim()
      }
    ];
    const nextMessages = [...outboundMessages];

    let pendingAssistantId = '';
    if (stream) {
      pendingAssistantId = `assistant-${Date.now()}`;
      nextMessages.push({ id: pendingAssistantId, role: 'assistant', text: '', pending: true });
    }
    messages = nextMessages;
    const pendingInput = input.trim();
    input = '';

    try {
      const exchange = await requestGatewayExchange({
        target: $selectedTarget,
        apiKey: apiKey.trim(),
        protocol,
        provider,
        model: model.trim(),
        stream,
        mode,
        sessionId,
        injectContext,
        injectTools,
        toolWrite,
        memoryPersist: persistForTurn,
        endConversation,
        messages: outboundMessages,
        onText: (delta) => {
          messages = messages.map((message) => {
            if (message.id !== pendingAssistantId) {
              return message;
            }
            return { ...message, text: `${message.text}${delta}` };
          });
        },
        onUsage: (usage) => {
          messages = messages.map((message) => {
            if (message.id !== pendingAssistantId) {
              return message;
            }
            return { ...message, usage };
          });
        },
        onRawEvent: (event) => {
          debug = { ...debug, rawEvents: [...debug.rawEvents, event] };
        }
      });

      debug = exchange.debug;
      gatewayHeaders = exchange.headers;
      sessionId = exchange.headers.conversationId || sessionId;

      if (stream) {
        messages = messages.map((message) => {
          if (message.id !== pendingAssistantId) {
            return message;
          }
          return { ...exchange.assistant, id: pendingAssistantId, pending: false };
        });
      } else {
        messages = [...nextMessages, exchange.assistant];
      }

      latestTurnPrompt = pendingInput;
      latestTurnPersistMode = persistForTurn;
      completedTurns += 1;
      await refreshDebugPanels(0, persistForTurn, outboundMessages);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Gateway request failed';
      if (pendingAssistantId) {
        messages = messages.filter((message) => message.id !== pendingAssistantId);
      }
      conversationMemory = previousConversationMemory;
      memoryPreview = previousMemoryPreview;
      input = pendingInput;
    } finally {
      sending = false;
    }
  }

  function messagesForPreview(source: ChatThreadMessage[]): ChatThreadMessage[] {
    const next = [...source];
    while (next.length > 0 && next[next.length - 1]?.role === 'assistant') {
      next.pop();
    }
    return next;
  }

  async function refreshDebugPanels(
    attempt = 0,
    persistMode = debugMemoryPersistMode,
    previewMessages: ChatThreadMessage[] = messagesForPreview(messages)
  ): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !gatewayHeaders.conversationId) {
      return;
    }
    try {
      conversationDetail = await fetchConversation($selectedTarget, gatewayHeaders.conversationId);
      injectedMemory = (conversationDetail.metadata?.injection as Record<string, unknown>) ?? null;

      if (persistMode) {
        conversationMemory = await fetchConversationMemory($selectedTarget, gatewayHeaders.conversationId);
        memoryPreview = null;
        const pendingStatuses = new Set(['pending', 'queued', 'running']);
        if (
          attempt < 3 &&
          conversationDetail &&
          (pendingStatuses.has(conversationDetail.summary_status) || pendingStatuses.has(conversationDetail.analysis_status))
        ) {
          setTimeout(() => {
            void refreshDebugPanels(attempt + 1, persistMode);
          }, 1500);
        }
      } else {
        memoryPreview = await fetchMemoryPreview($selectedTarget, gatewayHeaders.conversationId, previewMessages);
        conversationMemory = null;
      }
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load debug panels';
    }
  }

  function usageSummary(usage?: UsageRecord): string {
    if (!usage) {
      return 'No usage yet';
    }
    const total = usage.total_tokens ?? usage.totalTokens ?? 0;
    const inputTokens = usage.input_tokens ?? usage.inputTokens ?? 0;
    const outputTokens = usage.output_tokens ?? usage.outputTokens ?? 0;
    return `${inputTokens} in / ${outputTokens} out / ${total} total`;
  }

  function changeTone(change: MemoryTurnChange): 'success' | 'brand' | 'danger' {
    if (change === 'added') {
      return 'success';
    }
    if (change === 'updated') {
      return 'brand';
    }
    return 'danger';
  }

  function changeLabel(change: MemoryTurnChange): string {
    if (change === 'added') {
      return 'Added';
    }
    if (change === 'updated') {
      return 'Updated';
    }
    return 'Removed';
  }

  function kindLabel(kind: MemoryTurnKind): string {
    switch (kind) {
      case 'injected':
        return 'Injected';
      case 'document':
        return 'Document';
      case 'key_memory':
        return 'Key Memory';
      case 'preview':
        return 'Preview';
      case 'keyword':
        return 'Keyword';
      default:
        return 'Memory';
    }
  }

  function pluralize(value: number, noun: string): string {
    return `${value} ${noun}${value === 1 ? '' : 's'}`;
  }

  function injectionItems(payload: Record<string, unknown> | null): Array<Record<string, unknown>> {
    return recordList(payload?.items);
  }

  function numericLabel(value: unknown): string {
    const normalized = typeof value === 'number' ? value : Number(value ?? 0);
    return Number.isFinite(normalized) ? normalized.toFixed(3) : '0.000';
  }

  function dateTimeLabel(value: unknown): string {
    if (typeof value !== 'string' || !value) {
      return 'n/a';
    }
    const parsed = new Date(value);
    return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Playground / Chat</span>
    <h1 class="page-title">Drive the gateway directly and inspect memory behavior turn by turn</h1>
    <p class="page-description">
      Left side: live chat thread with streaming markdown. Right side: request controls, injected memory, extracted memory preview, and raw protocol diagnostics.
    </p>
  </div>

  <div class="chat-layout">
    <section class="chat-thread-card card">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Conversation</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Gateway thread</h2>
          <p class="page-description">Session id: {sessionId}</p>
        </div>
        <div class="toolbar">
          <StatusBadge tone={memoryPersist ? 'success' : 'warning'} label={memoryPersist ? 'Memory Persist On' : 'Memory Persist Off'} />
          <StatusBadge tone={stream ? 'brand' : 'muted'} label={stream ? 'Streaming' : 'Buffered'} />
          <button class="button-secondary" type="button" on:click={resetThread}>Reset thread</button>
        </div>
      </div>

      <div class="chat-thread panel">
        <ScrollArea.Root style="height: 58vh;">
          <ScrollArea.Viewport style="height: 100%; padding: 10px;">
            <div class="chat-list">
              {#if messages.length === 0}
                <div class="empty-state">Send a request to start the playground session.</div>
              {:else}
                {#each messages as message}
                  <ChatMessageCard {message} />
                {/each}
              {/if}
            </div>
          </ScrollArea.Viewport>
          <ScrollArea.Scrollbar orientation="vertical">
            <ScrollArea.Thumb />
          </ScrollArea.Scrollbar>
        </ScrollArea.Root>
      </div>

      <div class="composer">
        <label class="field">
          <span class="field-label">Message</span>
          <textarea class="textarea" bind:value={input} placeholder="Ask the target to recall memory, test a tool, or verify persistence behavior."></textarea>
        </label>
        {#if error}
          <div class="error-state">{error}</div>
        {/if}
        <div class="inline-group" style="justify-content: space-between;">
          <div class="helper-text">
            Memory persistence defaults to off for safe experiments. When it is off, tool memory writes are also blocked server-side.
          </div>
          <div class="inline-group">
            <button class="button-secondary" type="button" disabled={sending} on:click={() => submit(true)}>Send and End</button>
            <button class="button" type="button" disabled={sending} on:click={() => submit(false)}>{sending ? 'Sending...' : 'Send'}</button>
          </div>
        </div>
      </div>
    </section>

    <aside class="debug-column">
      <div class="debug-block">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Request Controls</div>
            <h2 class="page-title" style="font-size: 1.2rem;">Protocol and session settings</h2>
          </div>
        </div>

        <div class="toolbar">
          <label class="field">
            <span class="field-label">Gateway API key</span>
            <input class="input" bind:value={apiKey} type="password" autocomplete="off" placeholder="ms-..." />
          </label>
          <label class="field">
            <span class="field-label">Model</span>
            <input class="input" bind:value={model} placeholder="Configured model name" />
          </label>
          <label class="field">
            <span class="field-label">Protocol</span>
            <Select.Root type="single" bind:value={protocol} items={protocolItems}>
              <Select.Trigger class="select-trigger">{protocolItems.find((item) => item.value === protocol)?.label ?? 'Select protocol'}</Select.Trigger>
              <Select.Portal>
                <Select.Content class="select-content" sideOffset={8}>
                  <Select.Viewport>
                    {#each protocolItems as item}
                      <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                    {/each}
                  </Select.Viewport>
                </Select.Content>
              </Select.Portal>
            </Select.Root>
          </label>
          <label class="field">
            <span class="field-label">Provider override</span>
            <Select.Root type="single" bind:value={provider} items={providerItems}>
              <Select.Trigger class="select-trigger">{providerItems.find((item) => item.value === provider)?.label ?? 'Auto select'}</Select.Trigger>
              <Select.Portal>
                <Select.Content class="select-content" sideOffset={8}>
                  <Select.Viewport>
                    {#each providerItems as item}
                      <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                    {/each}
                  </Select.Viewport>
                </Select.Content>
              </Select.Portal>
            </Select.Root>
          </label>
          <label class="field">
            <span class="field-label">Session id</span>
            <input class="input" bind:value={sessionId} placeholder="playground-123" />
          </label>
          <div class="field">
            <span class="field-label">Mode</span>
            <div class="inline-group">
              <button class={mode === 'transparent' ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (mode = 'transparent')}>Transparent</button>
              <button class={mode === 'inject' ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (mode = 'inject')}>Inject</button>
            </div>
          </div>
          <div class="switch-row">
            <div>
              <div class="field-label">Inject Context</div>
              <div class="field-help">Add recalled memory into the system prompt.</div>
            </div>
            <Switch.Root class="switch-root" bind:checked={injectContext}><Switch.Thumb class="switch-thumb" /></Switch.Root>
          </div>
          <div class="switch-row">
            <div>
              <div class="field-label">Inject Tools</div>
              <div class="field-help">Expose memory recall tools to the model.</div>
            </div>
            <Switch.Root class="switch-root" bind:checked={injectTools}><Switch.Thumb class="switch-thumb" /></Switch.Root>
          </div>
          <div class="switch-row">
            <div>
              <div class="field-label">Tool Memory Write</div>
              <div class="field-help">Server still blocks writes when persistence is off.</div>
            </div>
            <Switch.Root class="switch-root" bind:checked={toolWrite}><Switch.Thumb class="switch-thumb" /></Switch.Root>
          </div>
          <div class="switch-row">
            <div>
              <div class="field-label">Memory Persist</div>
              <div class="field-help">Default off for safe testing sessions.</div>
            </div>
            <Switch.Root class="switch-root" bind:checked={memoryPersist}><Switch.Thumb class="switch-thumb" /></Switch.Root>
          </div>
          <div class="switch-row">
            <div>
              <div class="field-label">Stream</div>
              <div class="field-help">Keep markdown rendering live as deltas arrive.</div>
            </div>
            <Switch.Root class="switch-root" bind:checked={stream}><Switch.Thumb class="switch-thumb" /></Switch.Root>
          </div>
        </div>
      </div>

      <div class="debug-block">
        <Tabs.Root bind:value={debugTab}>
          <Tabs.List class="tabs-list">
            <Tabs.Trigger class="tabs-trigger" value="request">Injected</Tabs.Trigger>
            <Tabs.Trigger class="tabs-trigger" value="extracted">Extracted</Tabs.Trigger>
            <Tabs.Trigger class="tabs-trigger" value="diff">Diff</Tabs.Trigger>
            <Tabs.Trigger class="tabs-trigger" value="raw">Raw Debug</Tabs.Trigger>
          </Tabs.List>

          <Tabs.Content value="request" class="page-stack" style="margin-top: 14px;">
            {#if !$targetState.session.authenticated}
              <div class="notice-card">Admin login is required to load injected memory details from the target.</div>
            {:else if injectedMemory}
              {#if injectionItems(injectedMemory).length > 0}
                <div class="toolbar">
                  {#each injectionItems(injectedMemory) as item, index}
                    <div class="debug-block" style="background: var(--bg-panel-muted);">
                      <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                        <div class="page-stack" style="gap: 4px;">
                          <strong>{String(item.type ?? 'memory')}</strong>
                          <div class="inline-group" style="gap: 8px; flex-wrap: wrap;">
                            {#if item.injection_bucket}
                              <StatusBadge tone={String(item.injection_bucket) === 'key_memory_topk' ? 'warning' : 'brand'} label={String(item.injection_bucket)} />
                            {/if}
                            {#if item.time_tier}
                              <StatusBadge tone="muted" label={String(item.time_tier)} />
                            {/if}
                          </div>
                        </div>
                        <span class="subtle-text">#{index + 1}</span>
                      </div>
                      <div class="message-body">{String(item.content ?? '')}</div>
                      <div class="helper-text" style="margin-top: 10px;">
                        semantic {numericLabel(item.score)} · effective {numericLabel(item.effective_score)} · importance {numericLabel(item.importance)} · decay {numericLabel(item.decay_factor)}
                      </div>
                      <div class="helper-text">
                        activity {dateTimeLabel(item.activity_at)}{item.score_reason ? ` · ${String(item.score_reason)}` : ''}
                      </div>
                      <MemoryKeywordSignals
                        matchedKeywords={stringList(item.matched_keywords)}
                        sourceKeywords={stringList(item.keywords)}
                      />
                    </div>
                  {/each}
                </div>
                <Collapsible.Root>
                  <Collapsible.Trigger class="collapsible-trigger">Raw injection payload</Collapsible.Trigger>
                  <Collapsible.Content>
                    <div class="toolbar" style="margin-top: 10px;">
                      <JsonView value={injectedMemory} />
                    </div>
                  </Collapsible.Content>
                </Collapsible.Root>
              {:else}
                <JsonView value={injectedMemory} />
              {/if}
            {:else}
              <div class="empty-state">No injected memory has been recorded for this request yet.</div>
            {/if}
          </Tabs.Content>

          <Tabs.Content value="extracted" class="page-stack" style="margin-top: 14px;">
            {#if !$targetState.session.authenticated}
              <div class="notice-card">Admin login is required to load extracted memory state or preview output.</div>
            {:else if debugMemoryPersistMode}
              <div class="toolbar">
                <div class="inline-group">
                  <StatusBadge tone={conversationDetail?.summary_status === 'completed' ? 'success' : conversationDetail?.summary_status === 'failed' ? 'danger' : conversationDetail?.summary_status === 'skipped' ? 'muted' : 'warning'} label={`summary ${conversationDetail?.summary_status ?? 'n/a'}`} />
                  <StatusBadge tone={conversationDetail?.analysis_status === 'completed' ? 'success' : conversationDetail?.analysis_status === 'failed' ? 'danger' : conversationDetail?.analysis_status === 'skipped' ? 'muted' : 'warning'} label={`analysis ${conversationDetail?.analysis_status ?? 'n/a'}`} />
                </div>
                <button class="button-secondary" type="button" on:click={() => refreshDebugPanels()}>Refresh memory</button>
              </div>
              {#if (conversationMemory?.documents.length ?? 0) > 0 || (conversationMemory?.key_memories.length ?? 0) > 0}
                <div class="toolbar">
                  {#each conversationMemory?.documents ?? [] as item}
                    <div class="debug-block" style="background: var(--bg-panel-muted);">
                      <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                        <div class="page-stack" style="gap: 4px;">
                          <strong>{item.type}</strong>
                          <div class="inline-group" style="gap: 8px; flex-wrap: wrap;">
                            {#if item.time_tier}
                              <StatusBadge tone="muted" label={item.time_tier} />
                            {/if}
                            {#if item.effective_score !== undefined}
                              <StatusBadge tone="brand" label={`effective ${numericLabel(item.effective_score)}`} />
                            {/if}
                          </div>
                        </div>
                        <span class="subtle-text">{item.id}</span>
                      </div>
                      <div class="message-body">{item.content}</div>
                      <div class="helper-text">
                        activity {dateTimeLabel(item.activity_at)} · importance {numericLabel(item.importance)} · decay {numericLabel(item.decay_factor)}
                      </div>
                      <MemoryKeywordSignals
                        matchedKeywords={matchedKeywordsFromMetadata(item.metadata)}
                        sourceKeywords={sourceKeywordsFromMetadata(item.metadata)}
                      />
                    </div>
                  {/each}
                  {#each conversationMemory?.key_memories ?? [] as item}
                    <div class="debug-block" style="background: var(--bg-panel-muted);">
                      <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                        <div class="page-stack" style="gap: 4px;">
                          <strong>{item.key}</strong>
                          <div class="inline-group" style="gap: 8px; flex-wrap: wrap;">
                            {#if item.time_tier}
                              <StatusBadge tone="muted" label={item.time_tier} />
                            {/if}
                            {#if item.effective_score !== undefined}
                              <StatusBadge tone="warning" label={`effective ${numericLabel(item.effective_score)}`} />
                            {/if}
                          </div>
                        </div>
                        <span class="subtle-text">{item.id}</span>
                      </div>
                      <div class="message-body">{item.value}</div>
                      <div class="helper-text">
                        activity {dateTimeLabel(item.activity_at)} · importance {numericLabel(item.importance)} · decay {numericLabel(item.decay_factor)}
                      </div>
                    </div>
                  {/each}
                </div>
              {:else}
                <div class="empty-state">The server has not persisted conversation-linked memory documents yet.</div>
              {/if}
            {:else if memoryPreview}
              <MemoryKeywordSignals matchedKeywords={[]} sourceKeywords={memoryPreview.keywords ?? []} sourceLabel="Preview keyword extraction" />
              <JsonView value={memoryPreview} />
            {:else}
              <div class="empty-state">No extracted memory preview is available yet.</div>
            {/if}
          </Tabs.Content>

          <Tabs.Content value="diff" class="page-stack" style="margin-top: 14px;">
            {#if completedTurns === 0}
              <div class="empty-state">Send a request to compare this turn against the previous memory state.</div>
            {:else}
              <div class="toolbar">
                <div class="inline-group">
                  <StatusBadge tone={turnDiff.changeCount > 0 ? 'brand' : 'muted'} label={turnDiff.changeCount > 0 ? pluralize(turnDiff.changeCount, 'change') : 'No change detected'} />
                  <StatusBadge tone={debugMemoryPersistMode ? 'success' : 'warning'} label={debugMemoryPersistMode ? 'Persisted flow' : 'Preview flow'} />
                </div>
                <div class="helper-text">Latest turn #{completedTurns}: {latestTurnPrompt}</div>
              </div>

              {#if turnDiff.sections.length === 0}
                <div class="empty-state">No injected or extracted memory changed compared with the previous turn.</div>
              {:else}
                {#each turnDiff.sections as section}
                  <section class="memory-diff-section">
                    <div class="page-header" style="margin-bottom: 12px;">
                      <div>
                        <div class="sidebar-label">{section.title}</div>
                        <div class="field-help">{section.description}</div>
                      </div>
                      <StatusBadge tone="brand" label={pluralize(section.entries.length, 'change')} />
                    </div>

                    <div class="memory-diff-list">
                      {#each section.entries as entry}
                        <article class={`memory-diff-card ${entry.change}`}>
                          <div class="memory-diff-header">
                            <div class="page-stack" style="gap: 4px;">
                              <div class="inline-group" style="gap: 8px;">
                                <strong>{entry.title}</strong>
                                <StatusBadge tone={changeTone(entry.change)} label={changeLabel(entry.change)} />
                                <StatusBadge tone="muted" label={kindLabel(entry.kind)} />
                              </div>
                              {#if entry.subtitle}
                                <span class="subtle-text">{entry.subtitle}</span>
                              {/if}
                            </div>
                          </div>

                          {#if entry.before && entry.after && entry.before !== entry.after}
                            <div class="memory-diff-compare">
                              <div class="memory-diff-pane">
                                <div class="sidebar-label">Before</div>
                                <div class="memory-diff-content">{entry.before}</div>
                              </div>
                              <div class="memory-diff-pane">
                                <div class="sidebar-label">After</div>
                                <div class="memory-diff-content">{entry.after}</div>
                              </div>
                            </div>
                          {:else if entry.after}
                            <div class="memory-diff-content">{entry.after}</div>
                          {:else if entry.before}
                            <div class="memory-diff-content is-muted">{entry.before}</div>
                          {/if}

                          {#if entry.meta}
                            <div class="memory-diff-meta">
                              {#each Object.entries(entry.meta) as [key, value]}
                                <span class="memory-diff-chip">{key}: {value}</span>
                              {/each}
                            </div>
                          {/if}
                        </article>
                      {/each}
                    </div>
                  </section>
                {/each}
              {/if}
            {/if}
          </Tabs.Content>

          <Tabs.Content value="raw" class="page-stack" style="margin-top: 14px;">
            <div class="debug-block" style="background: var(--bg-panel-muted);">
              <div class="sidebar-label">Response headers</div>
              <JsonView value={gatewayHeaders} />
            </div>
            <Collapsible.Root>
              <Collapsible.Trigger class="collapsible-trigger">Request envelope</Collapsible.Trigger>
              <Collapsible.Content>
                <div class="toolbar" style="margin-top: 10px;">
                  <JsonView value={{ headers: debug.requestHeaders, body: debug.requestBody }} />
                </div>
              </Collapsible.Content>
            </Collapsible.Root>
            <Collapsible.Root>
              <Collapsible.Trigger class="collapsible-trigger">Raw stream or protocol events ({debug.rawEvents.length})</Collapsible.Trigger>
              <Collapsible.Content>
                <div class="toolbar" style="margin-top: 10px;">
                  <JsonView value={debug.rawEvents} emptyLabel="No raw protocol events captured." />
                </div>
              </Collapsible.Content>
            </Collapsible.Root>
            <div class="notice-card">Latest usage: {usageSummary(messages[messages.length - 1]?.usage)}</div>
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </aside>
  </div>
</section>
