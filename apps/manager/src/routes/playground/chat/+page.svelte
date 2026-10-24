<svelte:options runes={false} />

<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import Button from '#lib/components/Button.svelte';
  import TextareaField from '#lib/components/TextareaField.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import { onMount, tick } from 'svelte';
  import { ScrollArea, Tabs } from 'bits-ui';

  import {
    fetchConversation,
    fetchConversationMemory,
    fetchMemoryPreview,
    requestGatewayExchange
  } from '#lib/api/client.ts';
  import ChatMessageCard from '#lib/components/ChatMessageCard.svelte';
  import JsonView from '#lib/components/JsonView.svelte';
  import MemoryKeywordSignals from '#lib/components/MemoryKeywordSignals.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SegmentedField from '#lib/components/SegmentedField.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import SwitchField from '#lib/components/SwitchField.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { matchedKeywordsFromMetadata, recordList, sourceKeywordsFromMetadata, stringList } from '#lib/memory/keyword-signals.ts';
  import { buildMemoryTurnDiff, type MemoryTurnChange, type MemoryTurnDiffResult, type MemoryTurnKind } from '#lib/playground/memory-diff.ts';
  import {
    buildSessionDraftKey,
    draftsEqual,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '#lib/stores/session-drafts.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type {
    ChatThreadMessage,
    ConversationMemoryPayload,
    ConversationRecord,
    GatewayDebugState,
    GatewayProtocol,
    MemoryPreviewPayload,
    UsageRecord
  } from '#lib/types.ts';

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
  const modeItems = [
    { value: 'transparent', label: 'Transparent' },
    { value: 'inject', label: 'Inject' }
  ];
  const chatDraftVersion = 1;

  type GatewayHeaderState = {
    conversationId: string;
    requestId: string;
    memoryPersist: boolean;
  };

  type ChatPlaygroundThreadStatus = 'idle' | 'generating' | 'failed';

  type ChatPlaygroundThreadDraft = {
    threadId: string;
    protocol: GatewayProtocol;
    provider: string;
    mode: 'transparent' | 'inject';
    sessionId: string;
    model: string;
    apiKey: string;
    injectContext: boolean;
    injectTools: boolean;
    toolWrite: boolean;
    memoryPersist: boolean;
    stream: boolean;
    input: string;
    error: string;
    status: ChatPlaygroundThreadStatus;
    debugTab: string;
    messages: ChatThreadMessage[];
    debug: GatewayDebugState;
    gatewayHeaders: GatewayHeaderState;
    conversationDetail: ConversationRecord | null;
    conversationMemory: ConversationMemoryPayload | null;
    memoryPreview: MemoryPreviewPayload | null;
    injectedMemory: Record<string, unknown> | null;
    previousInjectedMemory: Record<string, unknown> | null;
    previousConversationMemory: ConversationMemoryPayload | null;
    previousMemoryPreview: MemoryPreviewPayload | null;
    latestTurnPrompt: string;
    completedTurns: number;
    latestTurnPersistMode: boolean;
    pendingRequestToken: string;
    latestTurnToken: string;
  };

  type ChatPlaygroundDraft = {
    activeThreadId: string;
    threads: ChatPlaygroundThreadDraft[];
  };

  type DebugRefreshOptions = {
    threadId?: string;
    attempt?: number;
    persistMode?: boolean;
    previewMessages?: ChatThreadMessage[];
    conversationId?: string;
    turnToken?: string;
  };

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
  let error = '';
  let threadStatus: ChatPlaygroundThreadStatus = 'idle';
  let pendingRequestToken = '';
  let latestTurnToken = '';
  let debugTab = 'request';
  let messages: ChatThreadMessage[] = [];
  let debug: GatewayDebugState = { requestHeaders: {}, requestBody: {}, rawEvents: [] };
  let gatewayHeaders: GatewayHeaderState = { conversationId: '', requestId: '', memoryPersist: false };
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
  let threads: ChatPlaygroundThreadDraft[] = [];
  let activeThreadId = '';
  let lastMessageCount = 0;
  let messageEndAnchor: HTMLDivElement | null = null;
  let activeDraftKey = '';
  let hydratingDraft = false;
  let hydratingActiveThread = false;
  let draftReady = false;
  let requestSequence = 0;
  let activeThreadSyncDeps: unknown[] = [];
  let chatDraftSnapshot = emptyPlaygroundDraft();

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
  $: if (messages.length !== lastMessageCount) {
    lastMessageCount = messages.length;
    void scrollToLatestMessage();
  }
  $: chatDraftKey = buildSessionDraftKey('playground.chat', $selectedTarget?.id);
  $: if (chatDraftKey !== activeDraftKey) {
    activeDraftKey = chatDraftKey;
    hydratingDraft = true;
    applyPlaygroundDraft(readSessionDraft<ChatPlaygroundDraft>(chatDraftKey, chatDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: activeThreadSyncDeps = [
    activeThreadId,
    protocol,
    provider,
    mode,
    sessionId,
    model,
    apiKey,
    injectContext,
    injectTools,
    toolWrite,
    memoryPersist,
    stream,
    input,
    error,
    threadStatus,
    pendingRequestToken,
    latestTurnToken,
    debugTab,
    messages,
    debug,
    gatewayHeaders,
    conversationDetail,
    conversationMemory,
    memoryPreview,
    injectedMemory,
    previousInjectedMemory,
    previousConversationMemory,
    previousMemoryPreview,
    latestTurnPrompt,
    completedTurns,
    latestTurnPersistMode
  ];
  $: if (draftReady && !hydratingDraft && !hydratingActiveThread) {
    void activeThreadSyncDeps;
    syncActiveThreadDraft();
  }
  $: chatDraftSnapshot = capturePlaygroundDraft();
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, chatDraftVersion, chatDraftSnapshot);
  }

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, chatDraftVersion, capturePlaygroundDraft());
    }
  });

  onMount(() => {
    if (threads.length === 0) {
      const initial = createThreadDraft();
      threads = [initial];
      activateThread(initial.threadId);
      return;
    }
    if (!sessionId) {
      sessionId = nextSessionId();
      syncActiveThreadDraft();
    }
  });

  useSessionDraftUnloadWarning(
    () => threads.some((thread) => isDirtyThread(thread)),
    'This playground thread is only saved in the current tab. Closing or reloading the page will discard it.'
  );

  function nextSessionId(): string {
    return `playground-${Date.now()}`;
  }

  function nextThreadId(): string {
    return `thread-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  }

  function nextRequestToken(): string {
    requestSequence += 1;
    return `playground-request-${requestSequence}-${Date.now()}`;
  }

  function emptyDebugState(): GatewayDebugState {
    return { requestHeaders: {}, requestBody: {}, rawEvents: [] };
  }

  function emptyGatewayHeaders(nextConversationId: string): GatewayHeaderState {
    return { conversationId: nextConversationId, requestId: '', memoryPersist: false };
  }

  function createThreadDraft(template: Partial<ChatPlaygroundThreadDraft> = {}): ChatPlaygroundThreadDraft {
    const nextConversationId = template.sessionId?.trim() || nextSessionId();
    return {
      threadId: template.threadId || nextThreadId(),
      protocol: template.protocol ?? 'chat_completions',
      provider: template.provider ?? '',
      mode: template.mode ?? 'inject',
      sessionId: nextConversationId,
      model: template.model ?? '',
      apiKey: template.apiKey ?? '',
      injectContext: template.injectContext ?? true,
      injectTools: template.injectTools ?? false,
      toolWrite: template.toolWrite ?? false,
      memoryPersist: template.memoryPersist ?? false,
      stream: template.stream ?? true,
      input: template.input ?? '',
      error: template.error ?? '',
      status: template.status ?? 'idle',
      debugTab: template.debugTab ?? 'request',
      messages: template.messages ?? [],
      debug: template.debug ?? emptyDebugState(),
      gatewayHeaders: template.gatewayHeaders?.conversationId
        ? template.gatewayHeaders
        : emptyGatewayHeaders(nextConversationId),
      conversationDetail: template.conversationDetail ?? null,
      conversationMemory: template.conversationMemory ?? null,
      memoryPreview: template.memoryPreview ?? null,
      injectedMemory: template.injectedMemory ?? null,
      previousInjectedMemory: template.previousInjectedMemory ?? null,
      previousConversationMemory: template.previousConversationMemory ?? null,
      previousMemoryPreview: template.previousMemoryPreview ?? null,
      latestTurnPrompt: template.latestTurnPrompt ?? '',
      completedTurns: template.completedTurns ?? 0,
      latestTurnPersistMode: template.latestTurnPersistMode ?? false,
      pendingRequestToken: template.pendingRequestToken ?? '',
      latestTurnToken: template.latestTurnToken ?? ''
    };
  }

  function normalizeLoadedThreadDraft(draft: Partial<ChatPlaygroundThreadDraft> | null | undefined): ChatPlaygroundThreadDraft {
    return createThreadDraft({
      ...draft,
      status: draft?.status === 'failed' ? 'failed' : 'idle',
      pendingRequestToken: ''
    });
  }

  function emptyPlaygroundDraft(template?: Partial<ChatPlaygroundThreadDraft>): ChatPlaygroundDraft {
    const thread = createThreadDraft(template);
    return {
      activeThreadId: thread.threadId,
      threads: [thread]
    };
  }

  function normalizePlaygroundDraft(draft: ChatPlaygroundDraft | null): ChatPlaygroundDraft {
    if (draft && typeof draft === 'object' && Array.isArray((draft as ChatPlaygroundDraft).threads)) {
      const nextThreads = (draft as ChatPlaygroundDraft).threads.map((thread) => normalizeLoadedThreadDraft(thread));
      const fallback = nextThreads[0] ?? createThreadDraft();
      return {
        activeThreadId:
          nextThreads.find((thread) => thread.threadId === (draft as ChatPlaygroundDraft).activeThreadId)?.threadId ??
          fallback.threadId,
        threads: nextThreads.length > 0 ? nextThreads : [fallback]
      };
    }

    return emptyPlaygroundDraft();
  }

  function captureActiveThreadDraft(): ChatPlaygroundThreadDraft | null {
    if (!activeThreadId) {
      return null;
    }
    return createThreadDraft({
      threadId: activeThreadId,
      protocol,
      provider,
      mode,
      sessionId,
      model,
      apiKey,
      injectContext,
      injectTools,
      toolWrite,
      memoryPersist,
      stream,
      input,
      error,
      status: threadStatus,
      debugTab,
      messages,
      debug,
      gatewayHeaders,
      conversationDetail,
      conversationMemory,
      memoryPreview,
      injectedMemory,
      previousInjectedMemory,
      previousConversationMemory,
      previousMemoryPreview,
      latestTurnPrompt,
      completedTurns,
      latestTurnPersistMode,
      pendingRequestToken,
      latestTurnToken
    });
  }

  function mergedThreadsWithActiveState(source: ChatPlaygroundThreadDraft[] = threads): ChatPlaygroundThreadDraft[] {
    const captured = captureActiveThreadDraft();
    if (!captured) {
      return source;
    }
    const index = source.findIndex((thread) => thread.threadId === captured.threadId);
    if (index < 0) {
      return [...source, captured];
    }
    if (draftsEqual(source[index], captured)) {
      return source;
    }
    const next = [...source];
    next[index] = captured;
    return next;
  }

  function capturePlaygroundDraft(): ChatPlaygroundDraft {
    const nextThreads = mergedThreadsWithActiveState();
    if (nextThreads.length === 0) {
      return emptyPlaygroundDraft();
    }
    return {
      activeThreadId: activeThreadId || nextThreads[0]?.threadId || '',
      threads: nextThreads
    };
  }

  function syncActiveThreadDraft(): void {
    const next = mergedThreadsWithActiveState();
    if (next !== threads) {
      threads = next;
    }
  }

  function applyThreadDraft(thread: ChatPlaygroundThreadDraft): void {
    const next = createThreadDraft(thread);
    protocol = next.protocol;
    provider = next.provider;
    mode = next.mode;
    sessionId = next.sessionId;
    model = next.model;
    apiKey = next.apiKey;
    injectContext = next.injectContext;
    injectTools = next.injectTools;
    toolWrite = next.toolWrite;
    memoryPersist = next.memoryPersist;
    stream = next.stream;
    input = next.input;
    error = next.error;
    threadStatus = next.status;
    pendingRequestToken = next.pendingRequestToken;
    latestTurnToken = next.latestTurnToken;
    debugTab = next.debugTab || 'request';
    messages = next.messages ?? [];
    debug = next.debug ?? emptyDebugState();
    gatewayHeaders = next.gatewayHeaders?.conversationId
      ? next.gatewayHeaders
      : emptyGatewayHeaders(next.sessionId);
    conversationDetail = next.conversationDetail ?? null;
    conversationMemory = next.conversationMemory ?? null;
    memoryPreview = next.memoryPreview ?? null;
    injectedMemory = next.injectedMemory ?? null;
    previousInjectedMemory = next.previousInjectedMemory ?? null;
    previousConversationMemory = next.previousConversationMemory ?? null;
    previousMemoryPreview = next.previousMemoryPreview ?? null;
    latestTurnPrompt = next.latestTurnPrompt ?? '';
    completedTurns = next.completedTurns ?? 0;
    latestTurnPersistMode = next.latestTurnPersistMode ?? false;
    lastMessageCount = messages.length;
  }

  function applyPlaygroundDraft(draft: ChatPlaygroundDraft | null): void {
    const next = normalizePlaygroundDraft(draft);
    const activeThread =
      next.threads.find((thread) => thread.threadId === next.activeThreadId) ??
      next.threads[0] ??
      createThreadDraft();

    hydratingActiveThread = true;
    threads = next.threads;
    activeThreadId = activeThread.threadId;
    applyThreadDraft(activeThread);
    hydratingActiveThread = false;
  }

  function updateThread(
    threadId: string,
    updater: (thread: ChatPlaygroundThreadDraft) => ChatPlaygroundThreadDraft
  ): ChatPlaygroundThreadDraft | null {
    const index = threads.findIndex((thread) => thread.threadId === threadId);
    if (index < 0) {
      return null;
    }
    const current = threads[index];
    const next = createThreadDraft({ ...updater(current), threadId: current.threadId });
    if (draftsEqual(current, next)) {
      return current;
    }
    const nextThreads = [...threads];
    nextThreads[index] = next;
    threads = nextThreads;
    if (threadId === activeThreadId) {
      hydratingActiveThread = true;
      applyThreadDraft(next);
      hydratingActiveThread = false;
    }
    return next;
  }

  function updateThreadForRequest(
    threadId: string,
    requestToken: string,
    updater: (thread: ChatPlaygroundThreadDraft) => ChatPlaygroundThreadDraft
  ): ChatPlaygroundThreadDraft | null {
    return updateThread(threadId, (thread) => {
      if (thread.pendingRequestToken !== requestToken) {
        return thread;
      }
      return updater(thread);
    });
  }

  function activateThread(threadId: string): void {
    if (!threadId) {
      return;
    }
    const nextThread = threads.find((thread) => thread.threadId === threadId);
    if (!nextThread) {
      return;
    }
    if (threadId === activeThreadId) {
      if (!hydratingActiveThread) {
        hydratingActiveThread = true;
        applyThreadDraft(nextThread);
        hydratingActiveThread = false;
      }
      return;
    }
    syncActiveThreadDraft();
    hydratingActiveThread = true;
    activeThreadId = threadId;
    applyThreadDraft(nextThread);
    hydratingActiveThread = false;
  }

  function cloneCurrentThreadSettings(source: ChatPlaygroundThreadDraft): Partial<ChatPlaygroundThreadDraft> {
    return {
      protocol: source.protocol,
      provider: source.provider,
      mode: source.mode,
      model: source.model,
      apiKey: source.apiKey,
      injectContext: source.injectContext,
      injectTools: source.injectTools,
      toolWrite: source.toolWrite,
      memoryPersist: source.memoryPersist,
      stream: source.stream
    };
  }

  function createNewThread(): void {
    const current = captureActiveThreadDraft() ?? createThreadDraft();
    syncActiveThreadDraft();
    const nextThread = createThreadDraft(cloneCurrentThreadSettings(current));
    threads = [...threads, nextThread];
    activateThread(nextThread.threadId);
  }

  function resetThread(): void {
    if (threadStatus === 'generating') {
      return;
    }
    const current = captureActiveThreadDraft() ?? createThreadDraft();
    const nextConversationId = nextSessionId();
    updateThread(activeThreadId, () =>
      createThreadDraft({
        ...cloneCurrentThreadSettings(current),
        threadId: current.threadId,
        sessionId: nextConversationId,
        gatewayHeaders: emptyGatewayHeaders(nextConversationId),
        input: '',
        error: '',
        status: 'idle',
        debugTab: 'request',
        messages: [],
        debug: emptyDebugState(),
        conversationDetail: null,
        conversationMemory: null,
        memoryPreview: null,
        injectedMemory: null,
        previousInjectedMemory: null,
        previousConversationMemory: null,
        previousMemoryPreview: null,
        latestTurnPrompt: '',
        completedTurns: 0,
        latestTurnPersistMode: false,
        pendingRequestToken: '',
        latestTurnToken: ''
      })
    );
  }

  async function scrollToLatestMessage(): Promise<void> {
    await tick();
    messageEndAnchor?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }

  function handleComposerKeydown(event: KeyboardEvent): void {
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && threadStatus !== 'generating') {
      event.preventDefault();
      void submit(false);
    }
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
    if (threadStatus === 'generating') {
      return;
    }

    syncActiveThreadDraft();
    const currentThreadId = activeThreadId;
    const requestToken = nextRequestToken();
    const persistForTurn = memoryPersist;
    const nextSession = sessionId.trim() || nextSessionId();
    const pendingInput = input.trim();
    const outboundMessages = [
      ...messages,
      {
        id: `user-${Date.now()}`,
        role: 'user' as const,
        text: pendingInput
      }
    ];
    const nextMessages = [...outboundMessages];

    let pendingAssistantId = '';
    if (stream) {
      pendingAssistantId = `assistant-${Date.now()}`;
      nextMessages.push({ id: pendingAssistantId, role: 'assistant', text: '', pending: true });
    }
    updateThread(currentThreadId, (thread) =>
      createThreadDraft({
        ...thread,
        sessionId: nextSession,
        error: '',
        status: 'generating',
        pendingRequestToken: requestToken,
        latestTurnToken: requestToken,
        previousInjectedMemory: thread.injectedMemory,
        previousConversationMemory: thread.conversationMemory,
        previousMemoryPreview: thread.memoryPreview,
        memoryPreview: null,
        conversationMemory: null,
        messages: nextMessages,
        input: ''
      })
    );

    try {
      const exchange = await requestGatewayExchange({
        target: $selectedTarget,
        apiKey: apiKey.trim(),
        protocol,
        provider,
        model: model.trim(),
        stream,
        mode,
        sessionId: nextSession,
        injectContext,
        injectTools,
        toolWrite,
        memoryPersist: persistForTurn,
        endConversation,
        messages: outboundMessages,
        onText: (delta) => {
          updateThreadForRequest(currentThreadId, requestToken, (thread) =>
            createThreadDraft({
              ...thread,
              messages: thread.messages.map((message) => {
                if (message.id !== pendingAssistantId) {
                  return message;
                }
                return { ...message, text: `${message.text}${delta}` };
              })
            })
          );
        },
        onUsage: (usage) => {
          updateThreadForRequest(currentThreadId, requestToken, (thread) =>
            createThreadDraft({
              ...thread,
              messages: thread.messages.map((message) => {
                if (message.id !== pendingAssistantId) {
                  return message;
                }
                return { ...message, usage };
              })
            })
          );
        },
        onRawEvent: (event) => {
          updateThreadForRequest(currentThreadId, requestToken, (thread) =>
            createThreadDraft({
              ...thread,
              debug: {
                ...thread.debug,
                rawEvents: [...thread.debug.rawEvents, event]
              }
            })
          );
        }
      });

      const nextConversationId = exchange.headers.conversationId || nextSession;
      const updatedThread = updateThreadForRequest(currentThreadId, requestToken, (thread) =>
        createThreadDraft({
          ...thread,
          debug: exchange.debug,
          gatewayHeaders: exchange.headers,
          sessionId: nextConversationId,
          messages: stream
            ? thread.messages.map((message) => {
                if (message.id !== pendingAssistantId) {
                  return message;
                }
                return { ...exchange.assistant, id: pendingAssistantId, pending: false };
              })
            : [...outboundMessages, exchange.assistant],
          latestTurnPrompt: pendingInput,
          latestTurnPersistMode: persistForTurn,
          completedTurns: thread.completedTurns + 1,
          error: '',
          status: 'idle',
          pendingRequestToken: ''
        })
      );

      if (!updatedThread) {
        return;
      }

      await refreshDebugPanels({
        threadId: currentThreadId,
        attempt: 0,
        persistMode: persistForTurn,
        previewMessages: outboundMessages,
        conversationId: nextConversationId,
        turnToken: requestToken
      });
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Gateway request failed';
      updateThreadForRequest(currentThreadId, requestToken, (thread) =>
        createThreadDraft({
          ...thread,
          error: message,
          status: 'failed',
          pendingRequestToken: '',
          messages: pendingAssistantId
            ? thread.messages.filter((item) => item.id !== pendingAssistantId)
            : thread.messages,
          conversationMemory: thread.previousConversationMemory,
          memoryPreview: thread.previousMemoryPreview,
          input: pendingInput
        })
      );
    }
  }

  function messagesForPreview(source: ChatThreadMessage[]): ChatThreadMessage[] {
    const next = [...source];
    while (next.length > 0 && next[next.length - 1]?.role === 'assistant') {
      next.pop();
    }
    return next;
  }

  async function refreshDebugPanels({
    threadId = activeThreadId,
    attempt = 0,
    persistMode = debugMemoryPersistMode,
    previewMessages = messagesForPreview(messages),
    conversationId,
    turnToken = ''
  }: DebugRefreshOptions = {}): Promise<void> {
    const targetThread = threads.find((thread) => thread.threadId === threadId);
    const nextConversationId = conversationId || targetThread?.gatewayHeaders.conversationId || '';
    if (!$selectedTarget || !$targetState.session.authenticated || !targetThread || !nextConversationId) {
      return;
    }
    try {
      const isCurrentTurn = () => {
        const current = threads.find((thread) => thread.threadId === threadId);
        if (!current) {
          return false;
        }
        return !turnToken || current.latestTurnToken === turnToken;
      };

      if (!isCurrentTurn()) {
        return;
      }

      const detail = await fetchConversation($selectedTarget, nextConversationId);
      if (!isCurrentTurn()) {
        return;
      }

      updateThread(threadId, (thread) =>
        createThreadDraft({
          ...thread,
          conversationDetail: detail,
          injectedMemory: (detail.metadata?.injection as Record<string, unknown>) ?? null
        })
      );

      if (persistMode) {
        const memory = await fetchConversationMemory($selectedTarget, nextConversationId);
        if (!isCurrentTurn()) {
          return;
        }
        updateThread(threadId, (thread) =>
          createThreadDraft({
            ...thread,
            conversationMemory: memory,
            memoryPreview: null
          })
        );
        const pendingStatuses = new Set(['pending', 'queued', 'running']);
        if (
          attempt < 3 &&
          detail &&
          (pendingStatuses.has(detail.summary_status) || pendingStatuses.has(detail.analysis_status))
        ) {
          window.setTimeout(() => {
            void refreshDebugPanels({
              threadId,
              attempt: attempt + 1,
              persistMode,
              previewMessages,
              conversationId: nextConversationId,
              turnToken
            });
          }, 1500);
        }
      } else {
        const preview = await fetchMemoryPreview($selectedTarget, nextConversationId, previewMessages);
        if (!isCurrentTurn()) {
          return;
        }
        updateThread(threadId, (thread) =>
          createThreadDraft({
            ...thread,
            memoryPreview: preview,
            conversationMemory: null
          })
        );
      }
    } catch (cause) {
      updateThread(threadId, (thread) =>
        createThreadDraft({
          ...thread,
          error: cause instanceof Error ? cause.message : 'Failed to load debug panels'
        })
      );
    }
  }

  function isDirtyThread(thread: ChatPlaygroundThreadDraft): boolean {
    return Boolean(
      thread.input.trim() ||
        thread.messages.length > 0 ||
        thread.apiKey.trim() ||
        thread.model.trim() ||
        thread.provider.trim() ||
        thread.protocol !== 'chat_completions' ||
        thread.mode !== 'inject' ||
        !thread.injectContext ||
        thread.injectTools ||
        thread.toolWrite ||
        thread.memoryPersist ||
        !thread.stream ||
        thread.debugTab !== 'request' ||
        thread.completedTurns > 0
    );
  }

  function isThreadGenerating(thread: ChatPlaygroundThreadDraft): boolean {
    return thread.status === 'generating';
  }

  function threadTitle(thread: ChatPlaygroundThreadDraft): string {
    const latestUserMessage = [...thread.messages].reverse().find((message) => message.role === 'user' && message.text.trim());
    const source = latestUserMessage?.text?.trim() || thread.sessionId;
    return source.length > 48 ? `${source.slice(0, 45)}...` : source;
  }

  function threadSubtitle(thread: ChatPlaygroundThreadDraft): string {
    const parts = [protocolLabel(thread.protocol)];
    if (thread.status === 'generating') {
      parts.push('Generating');
    } else if (thread.status === 'failed') {
      parts.push('Needs retry');
    } else if (thread.completedTurns > 0) {
      parts.push(`${thread.completedTurns} turn${thread.completedTurns === 1 ? '' : 's'}`);
    } else {
      parts.push('Ready');
    }
    return parts.join(', ');
  }

  function threadCountLabel(thread: ChatPlaygroundThreadDraft): string {
    const count = thread.messages.length;
    return `${count} msg${count === 1 ? '' : 's'}`;
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

  function protocolLabel(value: GatewayProtocol): string {
    return protocolItems.find((item) => item.value === value)?.label ?? value;
  }

  function providerLabel(value: string): string {
    return providerItems.find((item) => item.value === value)?.label ?? 'Auto select';
  }

  function modeLabel(value: 'transparent' | 'inject'): string {
    return value === 'inject' ? 'Memory inject' : 'Transparent';
  }

  function playgroundStatusLine(): string {
    const parts = [
      $selectedTarget?.label ?? 'No Target',
      pluralize(threads.length, 'thread'),
      protocolLabel(protocol),
      modeLabel(mode)
    ];
    return parts.join(', ');
  }

  function threadMetaLine(): string {
    const parts = [model.trim() || 'No model', providerLabel(provider), stream ? 'Streaming' : 'Buffered'];
    if (threadStatus === 'generating') {
      parts.push('Generating');
    } else if (threadStatus === 'failed') {
      parts.push('Retry needed');
    }
    return parts.join(', ');
  }

  function runMetaLine(): string {
    const parts = [$selectedTarget?.label ?? 'No Target', sessionId];
    if (memoryPersist) {
      parts.push('Persist');
    }
    if (threadStatus === 'generating') {
      parts.push('Generating');
    } else if (threadStatus === 'failed') {
      parts.push('Failed');
    }
    return parts.join(', ');
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Playground"
    title="Chat"
    subtitle={playgroundStatusLine()}
    description="Send live gateway turns to validate proxy, injection, memory generation, and tool chain behavior."
  />

  <div class="chat-studio">
    <section class="card chat-shell">
      <aside class="chat-history-panel" aria-label="Conversation history">
        <div class="chat-history-header">
          <div class="chat-history-title">
            <h2 class="section-title">History</h2>
            <div class="panel-meta">{pluralize(threads.length, 'thread')}</div>
          </div>
          <Button class="btn-secondary btn-sm" type="button" aria-label="New thread" onclick={createNewThread}>New</Button>
        </div>

        <div class="chat-history-list" role="tablist" aria-label="Playground conversations">
          {#each threads as thread}
            <Button

              class="thread-pill {thread.threadId === activeThreadId ? 'active-thread-pill' : ''}"
              type="button"
              role="tab"
              aria-selected={thread.threadId === activeThreadId}
              title={thread.sessionId}
              onclick={() => activateThread(thread.threadId)}
            >
              <span class="thread-pill-title">{threadTitle(thread)}</span>
              <span class="thread-pill-meta">{threadSubtitle(thread)}</span>
              <span class="thread-pill-foot">
                <span>{threadCountLabel(thread)}</span>
                {#if isThreadGenerating(thread)}
                  <span class="thread-pill-status">Streaming</span>
                {:else if thread.status === 'failed' && thread.error}
                  <span class="thread-pill-status thread-pill-status-danger">Failed</span>
                {/if}
              </span>
            </Button>
          {/each}
        </div>
      </aside>

      <div class="chat-conversation-panel">
        <div class="chat-shell-header">
          <div class="chat-shell-title">
            <h2 class="section-title">Conversation</h2>
            <div class="panel-meta">{threadMetaLine()}</div>
          </div>
          <Button class="btn-secondary btn-sm" type="button" disabled={threadStatus === 'generating'} onclick={resetThread}>Reset</Button>
        </div>

        <div class="chat-thread-surface">
          <ScrollArea.Root style="height: 100%;">
            <ScrollArea.Viewport class="chat-thread-viewport">
              <div class="chat-list">
                {#if messages.length === 0}
                  <div class="chat-empty-state">
                    <div class="chat-empty-title">Start a conversation</div>
                    <div class="subtle-text">Send a live gateway turn and inspect memory behavior in real time.</div>
                  </div>
                {:else}
                  {#each messages as message}
                    <ChatMessageCard {message} />
                  {/each}
                {/if}
                <div bind:this={messageEndAnchor}></div>
              </div>
            </ScrollArea.Viewport>
            <ScrollArea.Scrollbar orientation="vertical">
              <ScrollArea.Thumb />
            </ScrollArea.Scrollbar>
          </ScrollArea.Root>
        </div>

        <form class="chat-composer-card" on:submit|preventDefault={() => submit(false)}>
          <TextareaField
            label="Message" hideLabel
            textareaClass="chat-composer-input"
            bind:value={input}
            placeholder="Message the target, test memory recall, or inspect persistence."
            onkeydown={handleComposerKeydown}
          />
          {#if error}
            <div class="field-error">{error}</div>
          {/if}
          <div class="chat-composer-footer">
            <div class="chat-composer-meta">
              <span>{messages.length} message{messages.length === 1 ? '' : 's'}</span>
              <span>Ctrl/Cmd + Enter to send</span>
            </div>
            <div class="inline-group">
              <Button class="btn-ghost btn-sm" type="button" disabled={threadStatus === 'generating'} onclick={() => submit(true)}>
                Send &amp; end
              </Button>
              <Button class="btn-primary" type="submit" disabled={threadStatus === 'generating' || !input.trim()}>
                {threadStatus === 'generating' ? 'Generating…' : 'Send'}
              </Button>
            </div>
          </div>
        </form>
      </div>
    </section>

    <aside class="chat-inspector-rail">
      <section class="card inspector-card">
        <div class="page-header playground-inspector-header">
          <div class="page-header-meta playground-heading-stack">
            <h2 class="section-title">Run</h2>
            <div class="panel-meta">{runMetaLine()}</div>
          </div>
        </div>

        <div class="playground-form-stack">
          <div class="playground-run-grid">
            <TextInputField label="Gateway API key" bind:value={apiKey} type="password" autocomplete="off" placeholder="ms-..." />
            <TextInputField label="Model" bind:value={model} placeholder="Configured model name" />
            <SelectField
              label="Protocol"
              items={protocolItems}
              bind:value={protocol}
              placeholder="Select protocol"
            />
            <SelectField
              label="Provider override"
              items={providerItems}
              bind:value={provider}
              placeholder="Auto select"
            />
            <div class="playground-span-2">
              <TextInputField label="Session id" bind:value={sessionId} placeholder="playground-123" inputClass="input-mono" />
            </div>
          </div>

          <SegmentedField label="Mode" bind:value={mode} items={modeItems} />

          <div class="playground-switch-grid">
            <SwitchField bind:checked={injectContext} label="Inject context" />
            <SwitchField bind:checked={injectTools} label="Inject tools" />
            <SwitchField bind:checked={toolWrite} label="Tool memory write" />
            <SwitchField bind:checked={memoryPersist} label="Memory persist" />
            <SwitchField bind:checked={stream} label="Stream" />
          </div>
        </div>
      </section>

      <section class="card inspector-card playground-inspect-card">
        <div class="page-header playground-inspector-header">
          <div class="page-header-meta playground-heading-stack">
            <h2 class="section-title">Inspect</h2>
            <div class="panel-meta">{usageSummary(messages[messages.length - 1]?.usage)}</div>
          </div>
        </div>

        <Tabs.Root bind:value={debugTab} class="playground-tabs">
          <Tabs.List class="tabs-list">
            <Tabs.Trigger class="tabs-trigger" value="request">Inject</Tabs.Trigger>
            <Tabs.Trigger class="tabs-trigger" value="extracted">Recall</Tabs.Trigger>
            <Tabs.Trigger class="tabs-trigger" value="diff">Diff</Tabs.Trigger>
            <Tabs.Trigger class="tabs-trigger" value="raw">Raw</Tabs.Trigger>
          </Tabs.List>

          <Tabs.Content value="request" class="page-stack" style="margin-top: 12px;">
            <div class="playground-inspect-panel">
              {#if !$targetState.session.authenticated}
                <div class="notice-card">Connect a target token to inspect injected memory.</div>
              {:else if injectedMemory}
                {#if injectionItems(injectedMemory).length > 0}
                  <div class="stack-list">
                    {#each injectionItems(injectedMemory) as item, index}
                      <div class="debug-block debug-block-muted">
                        <div class="inline-group" style="justify-content: space-between;">
                          <div class="page-header-meta">
                            <strong>{String(item.type ?? 'memory')}</strong>
                            <MetadataRow items={[item.injection_bucket ? String(item.injection_bucket) : null, item.time_tier ? String(item.time_tier) : null]} />
                          </div>
                          <span class="helper-text mono">#{index + 1}</span>
                        </div>
                        <div class="message-body">{String(item.content ?? '')}</div>
                        <MetadataRow class="mono" items={[`Semantic ${numericLabel(item.score)}`, `Effective ${numericLabel(item.effective_score)}`, `Importance ${numericLabel(item.importance)}`, `Decay ${numericLabel(item.decay_factor)}`]} />
                        <MetadataRow items={[`Activity ${dateTimeLabel(item.activity_at)}`, item.score_reason ? String(item.score_reason) : null]} />
                        <MemoryKeywordSignals
                          matchedKeywords={stringList(item.matched_keywords)}
                          sourceKeywords={stringList(item.keywords)}
                        />
                      </div>
                    {/each}
                  </div>
                  <CollapsibleSection title="Payload">
                      <div style="margin-top: 8px;">
                        <JsonView value={injectedMemory} />
                      </div>
                    </CollapsibleSection>
                {:else}
                  <JsonView value={injectedMemory} />
                {/if}
              {:else}
                <div class="empty-state">No injected memory yet.</div>
              {/if}
            </div>
          </Tabs.Content>

          <Tabs.Content value="extracted" class="page-stack" style="margin-top: 12px;">
            <div class="playground-inspect-panel">
              {#if !$targetState.session.authenticated}
                <div class="notice-card">Connect a target token to inspect extracted memory.</div>
              {:else if debugMemoryPersistMode}
                <div class="inline-group" style="justify-content: space-between;">
                  <div class="inline-group">
                    <StatusBadge tone={conversationDetail?.summary_status === 'completed' ? 'success' : conversationDetail?.summary_status === 'failed' ? 'danger' : conversationDetail?.summary_status === 'skipped' ? 'muted' : 'warning'} label={`summary ${conversationDetail?.summary_status ?? 'n/a'}`} />
                    <StatusBadge tone={conversationDetail?.analysis_status === 'completed' ? 'success' : conversationDetail?.analysis_status === 'failed' ? 'danger' : conversationDetail?.analysis_status === 'skipped' ? 'muted' : 'warning'} label={`analysis ${conversationDetail?.analysis_status ?? 'n/a'}`} />
                  </div>
                  <Button class="btn-secondary btn-sm" type="button" onclick={() => refreshDebugPanels()}>Refresh</Button>
                </div>
                {#if (conversationMemory?.documents.length ?? 0) > 0 || (conversationMemory?.key_memories.length ?? 0) > 0}
                  <div class="stack-list">
                    {#each conversationMemory?.documents ?? [] as item}
                      <div class="debug-block debug-block-muted">
                        <div class="inline-group" style="justify-content: space-between;">
                          <div class="page-header-meta">
                            <strong>{item.type}</strong>
                            <MetadataRow items={[item.time_tier, item.effective_score !== undefined && `Effective ${numericLabel(item.effective_score)}`]} />
                          </div>
                          <span class="helper-text mono">{item.id}</span>
                        </div>
                        <div class="message-body">{item.content}</div>
                        <MetadataRow class="mono" items={[`Activity ${dateTimeLabel(item.activity_at)}`, `Importance ${numericLabel(item.importance)}`, `Decay ${numericLabel(item.decay_factor)}`]} />
                        <MemoryKeywordSignals
                          matchedKeywords={matchedKeywordsFromMetadata(item.metadata)}
                          sourceKeywords={sourceKeywordsFromMetadata(item.metadata)}
                        />
                      </div>
                    {/each}
                    {#each conversationMemory?.key_memories ?? [] as item}
                      <div class="debug-block debug-block-muted">
                        <div class="inline-group" style="justify-content: space-between;">
                          <div class="page-header-meta">
                            <strong>{item.key}</strong>
                            <MetadataRow items={[item.time_tier, item.effective_score !== undefined && `Effective ${numericLabel(item.effective_score)}`]} />
                          </div>
                          <span class="helper-text mono">{item.id}</span>
                        </div>
                        <div class="message-body">{item.value}</div>
                        <MetadataRow class="mono" items={[`Activity ${dateTimeLabel(item.activity_at)}`, `Importance ${numericLabel(item.importance)}`, `Decay ${numericLabel(item.decay_factor)}`]} />
                      </div>
                    {/each}
                  </div>
                {:else}
                  <div class="empty-state">No persisted memory documents yet.</div>
                {/if}
              {:else if memoryPreview}
                <MemoryKeywordSignals matchedKeywords={[]} sourceKeywords={memoryPreview.keywords ?? []} sourceLabel="Preview keyword extraction" />
                <JsonView value={memoryPreview} />
              {:else}
                <div class="empty-state">No extracted preview yet.</div>
              {/if}
            </div>
          </Tabs.Content>

          <Tabs.Content value="diff" class="page-stack" style="margin-top: 12px;">
            <div class="playground-inspect-panel">
              {#if completedTurns === 0}
                <div class="empty-state">Send a request to compare memory changes.</div>
              {:else}
                <div class="inline-group" style="justify-content: space-between;">
                  <div class="inline-group">
                    <StatusBadge tone={turnDiff.changeCount > 0 ? 'brand' : 'muted'} label={turnDiff.changeCount > 0 ? pluralize(turnDiff.changeCount, 'change') : 'No change'} />
                    <StatusBadge tone={debugMemoryPersistMode ? 'success' : 'warning'} label={debugMemoryPersistMode ? 'Persist' : 'Preview'} />
                  </div>
                  <div class="helper-text">Turn #{completedTurns}: {latestTurnPrompt}</div>
                </div>

                {#if turnDiff.sections.length === 0}
                  <div class="empty-state">No memory changes detected.</div>
                {:else}
                  {#each turnDiff.sections as section}
                    <section class="memory-diff-section">
                      <div class="page-header">
                        <div class="page-header-meta">
                          <h3 class="section-title" style="font-size: var(--font-size-md);">{section.title}</h3>
                          <div class="helper-text">{section.description}</div>
                        </div>
                        <StatusBadge tone="brand" label={pluralize(section.entries.length, 'change')} />
                      </div>

                      <div class="memory-diff-list">
                        {#each section.entries as entry}
                          <article class={`memory-diff-card ${entry.change}`}>
                            <div class="memory-diff-header">
                              <div class="page-header-meta">
                                <div class="inline-group">
                                  <strong>{entry.title}</strong>
                                  <StatusBadge tone={changeTone(entry.change)} label={changeLabel(entry.change)} />
                                  <StatusBadge tone="muted" label={kindLabel(entry.kind)} />
                                </div>
                                {#if entry.subtitle}
                                  <span class="helper-text">{entry.subtitle}</span>
                                {/if}
                              </div>
                            </div>

                            {#if entry.before && entry.after && entry.before !== entry.after}
                              <div class="memory-diff-compare">
                                <div class="memory-diff-pane">
                                  <div class="field-label">Before</div>
                                  <div class="memory-diff-content">{entry.before}</div>
                                </div>
                                <div class="memory-diff-pane">
                                  <div class="field-label">After</div>
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
            </div>
          </Tabs.Content>

          <Tabs.Content value="raw" class="page-stack" style="margin-top: 12px;">
            <div class="playground-inspect-panel">
              <div class="debug-block debug-block-muted">
                <div class="field-label">Response headers</div>
                <JsonView value={gatewayHeaders} />
              </div>
              <CollapsibleSection title="Request">
                  <div style="margin-top: 8px;">
                    <JsonView value={{ headers: debug.requestHeaders, body: debug.requestBody }} />
                  </div>
                </CollapsibleSection>
              <CollapsibleSection title="Raw events" count={debug.rawEvents.length}>
                  <div style="margin-top: 8px;">
                    <JsonView value={debug.rawEvents} emptyLabel="No raw protocol events captured." />
                  </div>
                </CollapsibleSection>
            </div>
          </Tabs.Content>
        </Tabs.Root>
      </section>
    </aside>
  </div>
</section>

<style>
  /* Chat-specific styling lives in app.css. Component-local overrides go here if needed. */
</style>
