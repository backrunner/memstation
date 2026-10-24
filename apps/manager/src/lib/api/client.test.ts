import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('$app/env', () => ({ browser: false }));
import { get } from 'svelte/store';
import { markTargetSessionConnected, targetState } from '#lib/stores/targets.ts';
import { configureTargetCatalog } from '#lib/targets/catalog.ts';
import { deleteAdminKeyMemory, requestGatewayExchange, revokeUserAPIKey } from '#lib/api/client.ts';
import type { ChatThreadMessage, GatewayProtocol, JSONValue, MemStationTarget } from '#lib/types.ts';

const target: MemStationTarget = {
  id: 'fixture',
  label: 'Fixture',
  baseUrl: 'http://127.0.0.1:18080',
  environment: 'test',
  clusterName: 'standalone',
  region: 'local',
  kind: 'single'
};

beforeEach(() => configureTargetCatalog([target]));

afterEach(() => {
  vi.restoreAllMocks();
});

function gatewayFetchMock(payload: unknown) {
  return vi.fn(async () => new Response(JSON.stringify(payload), { status: 200, headers: { 'Content-Type': 'application/json' } }));
}

function gatewayBody(fetchMock: ReturnType<typeof vi.fn>) {
  const init = fetchMock.mock.calls[0]?.[1] as RequestInit | undefined;
  return JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>;
}

function firstFetchInit(fetchMock: ReturnType<typeof vi.fn>): RequestInit {
  return (fetchMock.mock.calls[0]?.[1] ?? {}) as RequestInit;
}

async function requestExchange(protocol: GatewayProtocol, messages: ChatThreadMessage[], payload: unknown) {
  const fetchMock = gatewayFetchMock(payload);
  vi.stubGlobal('fetch', fetchMock);

  const exchange = await requestGatewayExchange({
    target,
    apiKey: 'memstation-test-key',
    protocol,
    provider: 'openai',
    model: 'test-model',
    stream: false,
    mode: 'transparent',
    sessionId: 'session-1',
    injectContext: false,
    injectTools: false,
    toolWrite: false,
    memoryPersist: true,
    messages
  });

  return { exchange, fetchMock };
}

describe('api client delete flows', () => {
  it('treats 204 key memory deletion responses as success', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(deleteAdminKeyMemory(target, 'key-1', 'admin')).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledWith(
      '/manager/api/targets/fixture/api/admin/memory/keys/key-1?user_id=admin',
      expect.objectContaining({ method: 'DELETE' })
    );
    const init = firstFetchInit(fetchMock);
    expect(init.credentials).toBe('same-origin');
    expect(new Headers(init.headers).get('Authorization')).toBeNull();
  });

  it('treats 204 API key revoke responses as success', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(revokeUserAPIKey(target, 'admin', 'api-key-1')).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledWith(
      '/manager/api/targets/fixture/api/admin/users/admin/api-keys/api-key-1',
      expect.objectContaining({ method: 'DELETE' })
    );
    const init = firstFetchInit(fetchMock);
    expect(init.credentials).toBe('same-origin');
    expect(new Headers(init.headers).get('Authorization')).toBeNull();
  });
});

describe('admin session expiry', () => {
  it('shows reconnect on current 401 and ignores a response from before a new login', async () => {
    targetState.set({ loaded: true, targets: [target], selectedId: target.id, session: { authenticated: true, checking: false } });
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"message":"Reconnect this target."}', { status: 401 })));
    await expect(revokeUserAPIKey(target, 'admin', 'key')).rejects.toThrow('Reconnect');
    expect(get(targetState).session.authenticated).toBe(false);
    let release!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { release = resolve; })));
    const old = revokeUserAPIKey(target, 'admin', 'key');
    markTargetSessionConnected(target.id);
    release(new Response('{"message":"expired"}', { status: 401 }));
    await expect(old).rejects.toThrow('expired');
    expect(get(targetState).session.authenticated).toBe(true);
  });
});

describe('gateway multimodal round-trip', () => {
  it('preserves multimodal chat completion messages and tool outputs', async () => {
    const toolOutput: JSONValue = [
      { type: 'output_text', text: 'table extracted' },
      {
        type: 'input_image',
        image_url: { url: 'https://example.com/tool-output.png', mime_type: 'image/png' },
        text: 'tool-rendered chart'
      }
    ];

    const { exchange, fetchMock } = await requestExchange(
      'chat_completions',
      [
        {
          id: 'user-1',
          role: 'user',
          text: 'Please inspect this chart',
          blocks: [
            { type: 'text', text: 'Please inspect this chart' },
            { type: 'image', source_url: 'https://example.com/user-chart.png', mime_type: 'image/png', text: 'quarterly chart' }
          ]
        },
        {
          id: 'tool-1',
          role: 'tool',
          text: '',
          toolResults: [{ tool_call_id: 'call-1', output: toolOutput }]
        }
      ],
      {
        id: 'chat-response-1',
        choices: [
          {
            message: {
              role: 'assistant',
              content: [
                { type: 'output_text', text: 'I found the anomaly.' },
                {
                  type: 'file',
                  source_url: 'https://example.com/report.pdf',
                  mime_type: 'application/pdf',
                  filename: 'report.pdf',
                  text: 'Q4 report'
                }
              ]
            }
          }
        ],
        usage: { prompt_tokens: 11, completion_tokens: 7, total_tokens: 18 }
      }
    );

    const body = gatewayBody(fetchMock);
    const messages = body.messages as Array<Record<string, unknown>>;

    expect(messages[0]?.content).toEqual([
      { type: 'input_text', text: 'Please inspect this chart' },
      {
        type: 'input_image',
        image_url: { url: 'https://example.com/user-chart.png', mime_type: 'image/png' },
        text: 'quarterly chart'
      }
    ]);
    expect(messages[1]?.content).toEqual(toolOutput);

    expect(exchange.assistant.blocks).toEqual([
      { type: 'text', text: 'I found the anomaly.' },
      {
        type: 'file',
        source_url: 'https://example.com/report.pdf',
        mime_type: 'application/pdf',
        filename: 'report.pdf',
        text: 'Q4 report'
      }
    ]);
  });

  it('preserves responses tool payload JSON and keeps block-only assistant messages', async () => {
    const { exchange, fetchMock } = await requestExchange(
      'responses',
      [
        {
          id: 'assistant-1',
          role: 'assistant',
          text: '',
          blocks: [{ type: 'image', source_url: 'https://example.com/frame.png', mime_type: 'image/png', text: 'frame capture' }],
          toolCalls: [
            {
              id: 'call-vision',
              name: 'vision.analyze',
              arguments: {
                attachments: [{ type: 'image', source_url: 'https://example.com/arg-image.png', mime_type: 'image/png', text: 'tool arg image' }]
              }
            }
          ]
        },
        {
          id: 'tool-1',
          role: 'tool',
          text: '',
          toolResults: [
            {
              tool_call_id: 'call-vision',
              output: {
                segments: [{ type: 'input_audio', audio: { url: 'https://example.com/audio.mp3', mime_type: 'audio/mpeg' }, text: 'hello world' }]
              }
            }
          ]
        }
      ],
      {
        id: 'response-1',
        output: [
          {
            type: 'message',
            role: 'assistant',
            content: [
              { type: 'output_text', text: 'Transcript ready.' },
              {
                type: 'input_audio',
                audio: { url: 'https://example.com/audio.mp3', mime_type: 'audio/mpeg' },
                text: 'hello world'
              }
            ]
          }
        ],
        usage: { input_tokens: 9, output_tokens: 5, total_tokens: 14 }
      }
    );

    const body = gatewayBody(fetchMock);
    const input = body.input as Array<Record<string, unknown>>;

    expect(input[0]).toMatchObject({
      type: 'function_call',
      call_id: 'call-vision',
      name: 'vision.analyze'
    });
    expect(JSON.parse(String(input[0]?.arguments))).toEqual({
      attachments: [{ type: 'image', source_url: 'https://example.com/arg-image.png', mime_type: 'image/png', text: 'tool arg image' }]
    });
    expect(input[1]).toEqual({
      type: 'message',
      role: 'assistant',
      content: [{ type: 'input_image', image_url: { url: 'https://example.com/frame.png', mime_type: 'image/png' }, text: 'frame capture' }]
    });
    expect(input[2]).toEqual({
      type: 'function_call_output',
      call_id: 'call-vision',
      output: {
        segments: [{ type: 'input_audio', audio: { url: 'https://example.com/audio.mp3', mime_type: 'audio/mpeg' }, text: 'hello world' }]
      }
    });

    expect(exchange.assistant.blocks).toEqual([
      { type: 'text', text: 'Transcript ready.' },
      {
        type: 'audio',
        source_url: 'https://example.com/audio.mp3',
        mime_type: 'audio/mpeg',
        text: 'hello world'
      }
    ]);
  });

  it('preserves anthropic mixed content and parses assistant multimodal tool use', async () => {
    const toolResultContent: JSONValue = [
      {
        type: 'image',
        source_url: 'https://example.com/tool-result.png',
        mime_type: 'image/png',
        text: 'annotated result'
      }
    ];

    const { exchange, fetchMock } = await requestExchange(
      'anthropic_messages',
      [
        {
          id: 'user-1',
          role: 'user',
          text: 'Review this upload',
          blocks: [
            { type: 'text', text: 'Review this upload' },
            { type: 'file', source_url: 'https://example.com/scan.pdf', mime_type: 'application/pdf', filename: 'scan.pdf', text: 'scanned invoice' }
          ]
        },
        {
          id: 'tool-1',
          role: 'tool',
          text: '',
          toolResults: [{ tool_call_id: 'toolu-1', output: toolResultContent }]
        }
      ],
      {
        id: 'anthropic-response-1',
        content: [
          { type: 'text', text: 'Stored the evidence.' },
          { type: 'image', source_url: 'https://example.com/evidence.png', mime_type: 'image/png', text: 'evidence image' },
          {
            type: 'tool_use',
            id: 'toolu-2',
            name: 'memory.store',
            input: {
              attachments: [{ type: 'image', source_url: 'https://example.com/evidence.png', mime_type: 'image/png' }]
            }
          }
        ],
        usage: { input_tokens: 4, output_tokens: 6 }
      }
    );

    const body = gatewayBody(fetchMock);
    const messages = body.messages as Array<Record<string, unknown>>;

    expect(messages[0]?.content).toEqual([
      { type: 'text', text: 'Review this upload' },
      {
        type: 'document',
        source_url: 'https://example.com/scan.pdf',
        mime_type: 'application/pdf',
        filename: 'scan.pdf',
        text: 'scanned invoice'
      }
    ]);
    expect(messages[1]).toEqual({
      role: 'user',
      content: [{ type: 'tool_result', tool_use_id: 'toolu-1', content: toolResultContent }]
    });

    expect(exchange.assistant.blocks).toEqual([
      { type: 'text', text: 'Stored the evidence.' },
      {
        type: 'image',
        source_url: 'https://example.com/evidence.png',
        mime_type: 'image/png',
        text: 'evidence image'
      }
    ]);
    expect(exchange.assistant.toolCalls).toEqual([
      {
        id: 'toolu-2',
        name: 'memory.store',
        arguments: {
          attachments: [{ type: 'image', source_url: 'https://example.com/evidence.png', mime_type: 'image/png' }]
        }
      }
    ]);
  });
});
