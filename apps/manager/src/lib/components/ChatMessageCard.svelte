<svelte:options runes={false} />

<script lang="ts">
  import { Collapsible } from 'bits-ui';

  import JsonView from '$lib/components/JsonView.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { renderMarkdown } from '$lib/utils/markdown';
  import type { ChatThreadMessage, ContentBlock, JSONValue } from '$lib/types';

  export let message: ChatThreadMessage;

  interface ToolPayloadMediaEntry {
    key: string;
    label: string;
    block: ContentBlock;
  }

  let html = '';
  let roleTone: 'warning' | 'brand' | 'success' | 'danger' | 'muted' = 'muted';

  $: html = renderMarkdown(message.text);
  $: roleTone = message.role === 'assistant' ? 'brand' : message.role === 'user' ? 'success' : message.role === 'tool' ? 'warning' : 'muted';

  function blockText(block: ContentBlock): string {
    return block.text ?? block.raw ?? '';
  }

  function firstNonEmpty(...values: Array<string | undefined>): string | undefined {
    return values.find((value) => typeof value === 'string' && value.trim().length > 0);
  }

  function stringValue(value: unknown): string | undefined {
    return typeof value === 'string' && value.trim().length > 0 ? value : undefined;
  }

  function numberValue(value: unknown): number | undefined {
    return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
  }

  function recordValue(value: unknown): Record<string, unknown> | undefined {
    return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
  }

  function normalizePayloadValue(value: JSONValue | undefined): unknown {
    if (typeof value !== 'string') {
      return value;
    }
    const trimmed = value.trim();
    if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) {
      return value;
    }
    try {
      return JSON.parse(trimmed) as unknown;
    } catch {
      return value;
    }
  }

  function stringifyPayload(value: unknown): string {
    try {
      return JSON.stringify(value);
    } catch {
      return '';
    }
  }

  function inferPayloadBlockType(record: Record<string, unknown>, hint: string): ContentBlock['type'] | undefined {
    const type = stringValue(record.type)?.toLowerCase();
    switch (type) {
      case 'image':
      case 'image_url':
      case 'input_image':
        return 'image';
      case 'audio':
      case 'input_audio':
        return 'audio';
      case 'video':
      case 'input_video':
        return 'video';
      case 'file':
      case 'document':
      case 'input_file':
        return 'file';
    }

    switch (hint.toLowerCase()) {
      case 'image':
      case 'images':
      case 'image_url':
        return 'image';
      case 'audio':
      case 'audios':
        return 'audio';
      case 'video':
      case 'videos':
        return 'video';
      case 'file':
      case 'files':
      case 'document':
      case 'documents':
      case 'attachment':
      case 'attachments':
        return 'file';
    }

    const mimeType = stringValue(record.mime_type) ?? stringValue(record.media_type);
    if (mimeType?.startsWith('image/')) {
      return 'image';
    }
    if (mimeType?.startsWith('audio/')) {
      return 'audio';
    }
    if (mimeType?.startsWith('video/')) {
      return 'video';
    }
    if (
      stringValue(record.source_url) ||
      stringValue(record.url) ||
      stringValue(record.blob_id) ||
      stringValue(record.provider_file_id) ||
      stringValue(record.file_id) ||
      mimeType === 'application/pdf'
    ) {
      return 'file';
    }
    return undefined;
  }

  function payloadMediaBlock(record: Record<string, unknown>, hint: string): ContentBlock | undefined {
    const type = inferPayloadBlockType(record, hint);
    if (!type) {
      return undefined;
    }

    const imageSource = recordValue(record.image_url) ?? recordValue(record.image);
    const audioSource = recordValue(record.audio);
    const videoSource = recordValue(record.video);
    const fileSource = recordValue(record.file) ?? recordValue(record.document);
    const nestedSource = type === 'image' ? imageSource : type === 'audio' ? audioSource : type === 'video' ? videoSource : fileSource;

    const sourceURL = firstNonEmpty(
      stringValue(nestedSource?.url),
      stringValue(nestedSource?.source_url),
      stringValue(record.source_url),
      stringValue(record.url),
      typeof record.image_url === 'string' ? record.image_url : undefined
    );
    const providerFileID = firstNonEmpty(
      stringValue(nestedSource?.provider_file_id),
      stringValue(nestedSource?.file_id),
      stringValue(record.provider_file_id),
      stringValue(record.file_id)
    );
    const blobID = firstNonEmpty(stringValue(record.blob_id), stringValue(nestedSource?.blob_id));

    if (!sourceURL && !providerFileID && !blobID) {
      return undefined;
    }

    return {
      type,
      mime_type: firstNonEmpty(stringValue(nestedSource?.mime_type), stringValue(nestedSource?.media_type), stringValue(record.mime_type), stringValue(record.media_type)),
      source_url: sourceURL,
      provider_file_id: providerFileID,
      blob_id: blobID,
      filename: firstNonEmpty(stringValue(record.filename), stringValue(nestedSource?.filename)),
      page_index: numberValue(record.page_index),
      start_ms: numberValue(record.start_ms),
      end_ms: numberValue(record.end_ms),
      duration_ms: numberValue(record.duration_ms),
      text: firstNonEmpty(
        stringValue(record.text),
        stringValue(record.caption),
        stringValue(record.ocr_text),
        stringValue(record.transcript),
        stringValue(record.summary),
        stringValue(record.keyframe_summary),
        stringValue(record.document_text),
        stringValue(record.parsed_text)
      ),
      raw: stringifyPayload(record),
      metadata: recordValue(record.metadata)
    };
  }

  function toolPayloadMediaEntries(value: JSONValue | undefined, kind: 'input' | 'output'): ToolPayloadMediaEntry[] {
    const root = normalizePayloadValue(value);
    const entries: ToolPayloadMediaEntry[] = [];
    const seen = new Set<string>();

    const walk = (current: unknown, path: string, hint: string): void => {
      if (Array.isArray(current)) {
        current.forEach((item, index) => walk(item, `${path}[${index}]`, hint));
        return;
      }

      const record = recordValue(current);
      if (!record) {
        return;
      }

      const block = payloadMediaBlock(record, hint);
      if (block) {
        const key = `${kind}:${path}:${block.type}:${block.source_url ?? ''}:${block.provider_file_id ?? ''}:${block.blob_id ?? ''}`;
        if (!seen.has(key)) {
          seen.add(key);
          entries.push({
            key,
            label: `${kind === 'input' ? 'Tool input media' : 'Tool output media'} · ${path}`,
            block
          });
        }
        return;
      }

      Object.entries(record).forEach(([nestedKey, nestedValue]) => {
        walk(nestedValue, path ? `${path}.${nestedKey}` : nestedKey, nestedKey);
      });
    };

    walk(root, kind === 'input' ? 'tool_input' : 'tool_output', kind === 'input' ? 'attachments' : 'content');
    return entries;
  }
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

  {#if message.blocks?.length}
    <div class="toolbar" style="display: grid; gap: 10px; margin-top: 14px;">
      {#each message.blocks as block}
        {#if block.type === 'image' && block.source_url}
          <div class="debug-block">
            <img src={block.source_url} alt={block.filename ?? 'image block'} style="max-width: 100%; border-radius: 12px;" />
            {#if blockText(block)}
              <div class="subtle-text" style="margin-top: 8px;">{blockText(block)}</div>
            {/if}
          </div>
        {:else if block.type === 'audio' && block.source_url}
          <div class="debug-block">
            <audio controls style="width: 100%;" src={block.source_url}></audio>
            {#if blockText(block)}
              <div class="subtle-text" style="margin-top: 8px;">{blockText(block)}</div>
            {/if}
          </div>
        {:else if block.type === 'video' && block.source_url}
          <div class="debug-block">
            <!-- svelte-ignore a11y_media_has_caption -->
            <video controls style="width: 100%; border-radius: 12px;" src={block.source_url}></video>
            {#if blockText(block)}
              <div class="subtle-text" style="margin-top: 8px;">{blockText(block)}</div>
            {/if}
          </div>
        {:else if block.type === 'file'}
          <div class="debug-block">
            <div class="inline-group" style="justify-content: space-between;">
              <strong>{block.filename ?? 'file'}</strong>
              <span class="subtle-text">{block.mime_type ?? block.type}</span>
            </div>
            {#if block.source_url}
              <a class="link-inline" href={block.source_url} target="_blank" rel="noreferrer">Open source</a>
            {/if}
            {#if blockText(block)}
              <div class="subtle-text" style="margin-top: 8px;">{blockText(block)}</div>
            {/if}
          </div>
        {:else if block.type !== 'text' && blockText(block)}
          <div class="debug-block">
            <div class="inline-group" style="justify-content: space-between;">
              <strong>{block.type}</strong>
              <span class="subtle-text">{block.mime_type ?? 'derived text'}</span>
            </div>
            <div class="message-body">{@html renderMarkdown(blockText(block))}</div>
          </div>
        {/if}
      {/each}
    </div>
  {/if}

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
              <JsonView value={toolCall.arguments ?? null} />
              {#if toolPayloadMediaEntries(toolCall.arguments, 'input').length}
                <div class="toolbar" style="display: grid; gap: 8px; margin-top: 10px;">
                  {#each toolPayloadMediaEntries(toolCall.arguments, 'input') as entry}
                    <div class="debug-block" style="background: var(--bg-panel-muted);">
                      <div class="inline-group" style="justify-content: space-between;">
                        <strong>{entry.label}</strong>
                        <span class="subtle-text">{entry.block.mime_type ?? entry.block.type}</span>
                      </div>
                      {#if entry.block.type === 'image' && entry.block.source_url}
                        <img src={entry.block.source_url} alt={entry.block.filename ?? 'tool input image'} style="max-width: 100%; border-radius: 12px; margin-top: 8px;" />
                      {:else if entry.block.type === 'audio' && entry.block.source_url}
                        <audio controls style="width: 100%; margin-top: 8px;" src={entry.block.source_url}></audio>
                      {:else if entry.block.type === 'video' && entry.block.source_url}
                        <!-- svelte-ignore a11y_media_has_caption -->
                        <video controls style="width: 100%; border-radius: 12px; margin-top: 8px;" src={entry.block.source_url}></video>
                      {:else if entry.block.type === 'file'}
                        <div style="margin-top: 8px;">
                          <strong>{entry.block.filename ?? 'file'}</strong>
                          {#if entry.block.source_url}
                            <div><a class="link-inline" href={entry.block.source_url} target="_blank" rel="noreferrer">Open source</a></div>
                          {/if}
                        </div>
                      {/if}
                      {#if blockText(entry.block)}
                        <div class="subtle-text" style="margin-top: 8px;">{blockText(entry.block)}</div>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
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
              <JsonView value={toolResult.output ?? null} />
              {#if toolPayloadMediaEntries(toolResult.output, 'output').length}
                <div class="toolbar" style="display: grid; gap: 8px; margin-top: 10px;">
                  {#each toolPayloadMediaEntries(toolResult.output, 'output') as entry}
                    <div class="debug-block" style="background: var(--bg-panel-muted);">
                      <div class="inline-group" style="justify-content: space-between;">
                        <strong>{entry.label}</strong>
                        <span class="subtle-text">{entry.block.mime_type ?? entry.block.type}</span>
                      </div>
                      {#if entry.block.type === 'image' && entry.block.source_url}
                        <img src={entry.block.source_url} alt={entry.block.filename ?? 'tool output image'} style="max-width: 100%; border-radius: 12px; margin-top: 8px;" />
                      {:else if entry.block.type === 'audio' && entry.block.source_url}
                        <audio controls style="width: 100%; margin-top: 8px;" src={entry.block.source_url}></audio>
                      {:else if entry.block.type === 'video' && entry.block.source_url}
                        <!-- svelte-ignore a11y_media_has_caption -->
                        <video controls style="width: 100%; border-radius: 12px; margin-top: 8px;" src={entry.block.source_url}></video>
                      {:else if entry.block.type === 'file'}
                        <div style="margin-top: 8px;">
                          <strong>{entry.block.filename ?? 'file'}</strong>
                          {#if entry.block.source_url}
                            <div><a class="link-inline" href={entry.block.source_url} target="_blank" rel="noreferrer">Open source</a></div>
                          {/if}
                        </div>
                      {/if}
                      {#if blockText(entry.block)}
                        <div class="subtle-text" style="margin-top: 8px;">{blockText(entry.block)}</div>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
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
