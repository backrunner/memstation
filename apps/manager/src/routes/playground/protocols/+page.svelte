<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { Select, Switch } from 'bits-ui';

  import JsonView from '$lib/components/JsonView.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget } from '$lib/stores/targets';
  import type { GatewayProtocol } from '$lib/types';

  const protocolItems = [
    { value: 'chat_completions', label: 'OpenAI Chat Completions' },
    { value: 'responses', label: 'OpenAI Responses' },
    { value: 'anthropic_messages', label: 'Anthropic Messages' }
  ];

  const templates: Record<GatewayProtocol, string> = {
    chat_completions: JSON.stringify({
      model: 'configured-chat-model',
      stream: true,
      messages: [
        { role: 'user', content: 'Say hello, then describe whether you recall any personal preferences.' }
      ]
    }, null, 2),
    responses: JSON.stringify({
      model: 'configured-chat-model',
      stream: true,
      input: [
        {
          type: 'message',
          role: 'user',
          content: [
            { type: 'input_text', text: 'Summarize what this gateway request is testing.' }
          ]
        }
      ]
    }, null, 2),
    anthropic_messages: JSON.stringify({
      model: 'configured-anthropic-model',
      stream: true,
      messages: [
        { role: 'user', content: 'Explain what protocol compatibility this request is checking.' }
      ]
    }, null, 2)
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

  onMount(() => {
    sessionId = `protocol-${Date.now()}`;
  });

  $: if (!requestBody.trim()) {
    requestBody = templates[protocol];
  }

  function applyTemplate(nextProtocol: GatewayProtocol): void {
    protocol = nextProtocol;
    requestBody = templates[nextProtocol];
    responseBody = null;
    rawEvents = [];
    error = '';
  }

  function gatewayPath(value: GatewayProtocol): string {
    switch (value) {
      case 'responses':
        return '/v1/responses';
      case 'anthropic_messages':
        return '/v1/messages';
      default:
        return '/v1/chat/completions';
    }
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

    try {
      const response = await fetch(`${$selectedTarget.baseUrl}${gatewayPath(protocol)}`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${apiKey.trim()}`,
          'Content-Type': 'application/json',
          'X-MemStation-Mode': mode,
          'X-MemStation-Session': sessionId,
          'X-MemStation-Memory-Persist': String(memoryPersist),
          'X-MemStation-Inject-Context': String(injectContext),
          'X-MemStation-Inject-Tools': String(injectTools),
          'X-MemStation-Tool-Write': String(toolWrite),
          ...(provider.trim() ? { 'X-MemStation-Provider': provider.trim() } : {})
        },
        body: JSON.stringify(body)
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
      }
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Protocol request failed';
    } finally {
      sending = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Playground / Protocols</span>
    <h1 class="page-title">Inspect raw protocol compatibility and SSE wire behavior</h1>
    <p class="page-description">
      Edit the exact request payload, send it straight to the gateway, and compare raw JSON or SSE events without any higher-level manager parsing.
    </p>
  </div>

  <div class="grid-2" style="align-items: start;">
    <section class="card">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Request editor</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Protocol template and headers</h2>
        </div>
        <button class="button-secondary" type="button" on:click={() => applyTemplate(protocol)}>Reset template</button>
      </div>

      <div class="toolbar">
        <label class="field">
          <span class="field-label">API key</span>
          <input class="input" bind:value={apiKey} type="password" autocomplete="off" placeholder="ms-..." />
        </label>
        <label class="field">
          <span class="field-label">Protocol</span>
          <Select.Root type="single" bind:value={protocol} items={protocolItems} onValueChange={(value) => applyTemplate(value as GatewayProtocol)}>
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
          <input class="input" bind:value={provider} placeholder="openai / anthropic / gemini" />
        </label>
        <label class="field">
          <span class="field-label">Session id</span>
          <input class="input" bind:value={sessionId} placeholder="protocol-123" />
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
            <div class="field-label">Memory Persist</div>
            <div class="field-help">Server disables persistence when off.</div>
          </div>
          <Switch.Root class="switch-root" bind:checked={memoryPersist}><Switch.Thumb class="switch-thumb" /></Switch.Root>
        </div>
        <div class="switch-row">
          <div>
            <div class="field-label">Inject Context</div>
            <div class="field-help">Apply memory context headers.</div>
          </div>
          <Switch.Root class="switch-root" bind:checked={injectContext}><Switch.Thumb class="switch-thumb" /></Switch.Root>
        </div>
        <div class="switch-row">
          <div>
            <div class="field-label">Inject Tools</div>
            <div class="field-help">Expose recall tools to the model.</div>
          </div>
          <Switch.Root class="switch-root" bind:checked={injectTools}><Switch.Thumb class="switch-thumb" /></Switch.Root>
        </div>
        <div class="switch-row">
          <div>
            <div class="field-label">Tool Memory Write</div>
            <div class="field-help">Still blocked if memory persistence is off.</div>
          </div>
          <Switch.Root class="switch-root" bind:checked={toolWrite}><Switch.Thumb class="switch-thumb" /></Switch.Root>
        </div>
        <label class="field">
          <span class="field-label">Request body</span>
          <textarea class="textarea" bind:value={requestBody} spellcheck="false"></textarea>
        </label>
        <button class="button" type="button" disabled={sending} on:click={sendRequest}>{sending ? 'Sending...' : 'Send raw request'}</button>
      </div>
    </section>

    <section class="card">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Response viewer</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Status, headers, body, and SSE events</h2>
        </div>
        {#if responseStatus > 0}
          <StatusBadge tone={responseStatus >= 200 && responseStatus < 300 ? 'success' : 'danger'} label={`HTTP ${responseStatus}`} />
        {/if}
      </div>

      {#if error}
        <div class="error-state">{error}</div>
      {/if}

      <div class="toolbar">
        <div class="debug-block">
          <div class="sidebar-label">Response headers</div>
          <JsonView value={responseHeaders} />
        </div>
        <div class="debug-block">
          <div class="sidebar-label">Response payload</div>
          <JsonView value={responseBody ?? {}} emptyLabel="No response yet" />
        </div>
        <div class="debug-block">
          <div class="sidebar-label">SSE / raw events</div>
          <JsonView value={rawEvents} emptyLabel="No raw events captured yet" />
        </div>
      </div>
    </section>
  </div>
</section>
