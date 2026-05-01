<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { Tabs } from 'bits-ui';

  import { requestGatewayRaw } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import SegmentedField from '$lib/components/SegmentedField.svelte';
  import SelectField from '$lib/components/SelectField.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import SwitchField from '$lib/components/SwitchField.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import TextareaField from '$lib/components/TextareaField.svelte';
  import {
    buildSessionDraftKey,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '$lib/stores/session-drafts';
  import { selectedTarget } from '$lib/stores/targets';
  import type { GatewayProtocol } from '$lib/types';

  const protocolItems = [
    { value: 'chat_completions', label: 'OpenAI Chat Completions' },
    { value: 'responses', label: 'OpenAI Responses' },
    { value: 'anthropic_messages', label: 'Anthropic Messages' }
  ];
  const modeItems = [
    { value: 'transparent', label: 'Transparent' },
    { value: 'inject', label: 'Inject' }
  ];
  const protocolDraftVersion = 1;

  type ProtocolPlaygroundDraft = {
    protocol: GatewayProtocol;
    apiKey: string;
    provider: string;
    mode: 'transparent' | 'inject';
    sessionId: string;
    memoryPersist: boolean;
    injectContext: boolean;
    injectTools: boolean;
    toolWrite: boolean;
    requestBody: string;
    responseStatus: number;
    responseHeaders: Record<string, string>;
    responseBody: Record<string, unknown> | string | null;
    rawEvents: Array<Record<string, unknown> | string>;
    responseTab: string;
  };

  const templates: Record<GatewayProtocol, string> = {
    chat_completions: JSON.stringify(
      {
        model: 'configured-chat-model',
        stream: true,
        messages: [{ role: 'user', content: 'Say hello, then describe whether you recall any personal preferences.' }]
      },
      null,
      2
    ),
    responses: JSON.stringify(
      {
        model: 'configured-chat-model',
        stream: true,
        input: [
          {
            type: 'message',
            role: 'user',
            content: [{ type: 'input_text', text: 'Summarize what this gateway request is testing.' }]
          }
        ]
      },
      null,
      2
    ),
    anthropic_messages: JSON.stringify(
      {
        model: 'configured-anthropic-model',
        stream: true,
        messages: [{ role: 'user', content: 'Explain what protocol compatibility this request is checking.' }]
      },
      null,
      2
    )
  };

  let protocol: GatewayProtocol = 'chat_completions';
  let apiKey = '';
  let provider = '';
  let mode: 'transparent' | 'inject' = 'transparent';
  let sessionId = '';
  let memoryPersist = false;
  let injectContext = false;
  let injectTools = false;
  let toolWrite = false;
  let requestBody = templates.chat_completions;
  let sending = false;
  let error = '';
  let responseStatus = 0;
  let responseHeaders: Record<string, string> = {};
  let responseBody: Record<string, unknown> | string | null = null;
  let rawEvents: Array<Record<string, unknown> | string> = [];
  let responseTab = 'payload';
  let templateProtocol: GatewayProtocol = protocol;
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let protocolDraftSnapshot = emptyProtocolDraft();

  $: protocolDraftKey = buildSessionDraftKey('playground.protocols', $selectedTarget?.id);
  $: if (protocolDraftKey !== activeDraftKey) {
    activeDraftKey = protocolDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<ProtocolPlaygroundDraft>(protocolDraftKey, protocolDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: protocolDraftSnapshot = {
    protocol,
    apiKey,
    provider,
    mode,
    sessionId,
    memoryPersist,
    injectContext,
    injectTools,
    toolWrite,
    requestBody,
    responseStatus,
    responseHeaders,
    responseBody,
    rawEvents,
    responseTab
  };
  $: responseIsEmpty =
    responseStatus === 0 &&
    responseBody == null &&
    Object.keys(responseHeaders).length === 0 &&
    rawEvents.length === 0;
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, protocolDraftVersion, protocolDraftSnapshot);
  }

  onMount(() => {
    if (!sessionId) {
      sessionId = nextSessionId();
    }
  });

  useSessionDraftUnloadWarning(
    () =>
      Boolean(
        apiKey.trim() ||
          provider.trim() ||
          protocol !== 'chat_completions' ||
          mode !== 'transparent' ||
          memoryPersist ||
          injectContext ||
          injectTools ||
          toolWrite ||
          responseStatus > 0 ||
          rawEvents.length > 0 ||
          requestBody.trim() !== templates[protocol].trim()
      ),
    'This protocol playground request is only saved in the current tab. Closing or reloading the page will discard it.'
  );

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, protocolDraftVersion, protocolDraftSnapshot);
    }
  });

  function nextSessionId(): string {
    return `protocol-${Date.now()}`;
  }

  function emptyProtocolDraft(): ProtocolPlaygroundDraft {
    return {
      protocol: 'chat_completions',
      apiKey: '',
      provider: '',
      mode: 'transparent',
      sessionId: nextSessionId(),
      memoryPersist: false,
      injectContext: false,
      injectTools: false,
      toolWrite: false,
      requestBody: templates.chat_completions,
      responseStatus: 0,
      responseHeaders: {},
      responseBody: null,
      rawEvents: [],
      responseTab: 'payload'
    };
  }

  function applyDraft(draft: ProtocolPlaygroundDraft | null): void {
    const next = draft ?? emptyProtocolDraft();
    protocol = next.protocol;
    apiKey = next.apiKey;
    provider = next.provider;
    mode = next.mode;
    sessionId = next.sessionId || nextSessionId();
    memoryPersist = next.memoryPersist;
    injectContext = next.injectContext;
    injectTools = next.injectTools;
    toolWrite = next.toolWrite;
    requestBody = next.requestBody || templates[next.protocol];
    responseStatus = next.responseStatus ?? 0;
    responseHeaders = next.responseHeaders ?? {};
    responseBody = next.responseBody ?? null;
    rawEvents = next.rawEvents ?? [];
    responseTab = next.responseTab || 'payload';
    templateProtocol = next.protocol;
    error = '';
    sending = false;
  }

  $: if (!requestBody.trim()) {
    requestBody = templates[protocol];
  }
  $: if (templateProtocol !== protocol) {
    templateProtocol = protocol;
    applyTemplate(protocol);
  }

  function applyTemplate(nextProtocol: GatewayProtocol): void {
    protocol = nextProtocol;
    requestBody = templates[nextProtocol];
    responseBody = null;
    rawEvents = [];
    responseHeaders = {};
    responseStatus = 0;
    responseTab = 'payload';
    error = '';
  }

  async function sendRequest(): Promise<void> {
    if (!$selectedTarget) {
      error = 'Select a target before sending a protocol request.';
      return;
    }
    if (!apiKey.trim()) {
      error = 'Provide a MemStation API key.';
      return;
    }

    let body: Record<string, unknown>;
    try {
      body = JSON.parse(requestBody) as Record<string, unknown>;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Request body must be valid JSON';
      return;
    }

    sending = true;
    error = '';
    responseBody = null;
    rawEvents = [];
    responseHeaders = {};
    responseStatus = 0;
    responseTab = 'payload';

    try {
      const response = await requestGatewayRaw({
        target: $selectedTarget,
        apiKey: apiKey.trim(),
        protocol,
        provider,
        mode,
        sessionId,
        memoryPersist,
        injectContext,
        injectTools,
        toolWrite,
        body
      });

      responseStatus = response.status;
      const nextHeaders: Record<string, string> = {};
      response.headers.forEach((value, key) => {
        nextHeaders[key] = value;
      });
      responseHeaders = nextHeaders;

      const isStream = Boolean(body.stream);
      if (!isStream) {
        const text = await response.text();
        try {
          responseBody = JSON.parse(text) as Record<string, unknown>;
        } catch {
          responseBody = text;
        }
      } else if (response.body) {
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        while (true) {
          const { done, value } = await reader.read();
          buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done });
          let separatorIndex = buffer.indexOf('\n\n');
          while (separatorIndex >= 0) {
            const frame = buffer.slice(0, separatorIndex);
            buffer = buffer.slice(separatorIndex + 2);
            const lines = frame.split(/\r?\n/).filter(Boolean);
            const dataLines = lines.filter((line) => line.startsWith('data:')).map((line) => line.slice(5).trim());
            const raw = dataLines.join('');
            if (raw && raw !== '[DONE]') {
              try {
                rawEvents = [...rawEvents, JSON.parse(raw) as Record<string, unknown>];
              } catch {
                rawEvents = [...rawEvents, raw];
              }
            }
            separatorIndex = buffer.indexOf('\n\n');
          }
          if (done) {
            if (buffer.trim()) {
              rawEvents = [...rawEvents, buffer.trim()];
            }
            break;
          }
        }
        responseBody = { streamed: true, event_count: rawEvents.length };
      }

      if (!response.ok) {
        error = typeof responseBody === 'string' ? responseBody : JSON.stringify(responseBody, null, 2);
        responseTab = 'payload';
      } else if (rawEvents.length > 0) {
        responseTab = 'raw';
      }
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Protocol request failed';
    } finally {
      sending = false;
    }
  }

  function protocolLabel(value: GatewayProtocol): string {
    return protocolItems.find((item) => item.value === value)?.label ?? value;
  }

  function headerLine(): string {
    const parts = [$selectedTarget?.label ?? 'No Target', protocolLabel(protocol), mode === 'inject' ? 'Inject' : 'Transparent'];
    if (sessionId) {
      parts.push(sessionId);
    }
    return parts.join(' · ');
  }

  function responseMeta(): string {
    if (!responseStatus) {
      return 'Send a raw gateway request to inspect headers, payload, and SSE frames.';
    }
    const pieces = [`HTTP ${responseStatus}`];
    if (rawEvents.length > 0) {
      pieces.push(`${rawEvents.length} raw frame${rawEvents.length === 1 ? '' : 's'}`);
    }
    return pieces.join(' · ');
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Playground"
    title="Protocols"
    subtitle={headerLine()}
    description="Compose raw gateway requests to verify protocol compatibility and inspect SSE frames."
  />

  <div class="page-columns">
    <div class="page-column">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Request</h2>
            <p class="panel-meta">{headerLine()}</p>
          </div>
          <div class="inline-group">
            <button class="btn-secondary btn-sm" type="button" on:click={() => applyTemplate(protocol)}>Reset template</button>
            <button class="btn-primary btn-sm" type="button" disabled={sending} on:click={sendRequest}>
              {sending ? 'Sending…' : 'Send request'}
            </button>
          </div>
        </div>

        {#if error}
          <div class="error-state">{error}</div>
        {/if}

        <div class="playground-form-stack">
          <div class="form-grid two-up">
            <TextInputField label="Gateway API key" bind:value={apiKey} type="password" autocomplete="off" placeholder="ms-..." />
            <SelectField label="Protocol" items={protocolItems} bind:value={protocol} placeholder="Select protocol" />
            <TextInputField label="Provider override" bind:value={provider} placeholder="openai / anthropic / gemini" />
            <TextInputField label="Session ID" bind:value={sessionId} placeholder="protocol-123" inputClass="input-mono" />
          </div>

          <SegmentedField label="Mode" bind:value={mode} items={modeItems} />

          <div class="playground-switch-grid">
            <SwitchField bind:checked={memoryPersist} label="Memory persist" />
            <SwitchField bind:checked={injectContext} label="Inject context" />
            <SwitchField bind:checked={injectTools} label="Inject tools" />
            <SwitchField bind:checked={toolWrite} label="Tool memory write" />
          </div>

          <TextareaField label="Request body" bind:value={requestBody} textareaClass="playground-json-textarea" spellcheck={false} />
        </div>
      </section>
    </div>

    <div class="page-column">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Response</h2>
            <p class="panel-meta">{responseMeta()}</p>
          </div>
          {#if responseStatus > 0}
            <StatusBadge
              tone={responseStatus >= 200 && responseStatus < 300 ? 'success' : 'danger'}
              label={`HTTP ${responseStatus}`}
            />
          {/if}
        </div>

        {#if responseIsEmpty}
          <div class="empty-state">Send a request to inspect the response payload, headers, and raw SSE frames.</div>
        {:else}
          <Tabs.Root bind:value={responseTab}>
            <Tabs.List class="tabs-list">
              <Tabs.Trigger class="tabs-trigger" value="payload">Payload</Tabs.Trigger>
              <Tabs.Trigger class="tabs-trigger" value="headers">Headers</Tabs.Trigger>
              <Tabs.Trigger class="tabs-trigger" value="raw">Raw</Tabs.Trigger>
            </Tabs.List>

            <Tabs.Content value="payload" class="page-stack" style="margin-top: 12px;">
              <JsonView value={responseBody ?? {}} emptyLabel="No response payload returned for this request." />
            </Tabs.Content>

            <Tabs.Content value="headers" class="page-stack" style="margin-top: 12px;">
              <JsonView value={responseHeaders} emptyLabel="No response headers returned for this request." />
            </Tabs.Content>

            <Tabs.Content value="raw" class="page-stack" style="margin-top: 12px;">
              <JsonView value={rawEvents} emptyLabel="No raw SSE frames captured for this request." />
            </Tabs.Content>
          </Tabs.Root>
        {/if}
      </section>
    </div>
  </div>
</section>
