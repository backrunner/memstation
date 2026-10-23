<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import { createMemoryRebuild, fetchConversation, fetchConversationMemory, fetchConversationSources, requeueTask } from '#lib/api/client.ts';
  import ChatMessageCard from '#lib/components/ChatMessageCard.svelte';
  import ConfirmActionDialog from '#lib/components/ConfirmActionDialog.svelte';
  import JsonView from '#lib/components/JsonView.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import { memoryCollectionHref, memoryDetailHref, memoryRouteKindFromDocumentType } from '#lib/memory/routes.ts';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { ConversationMemoryPayload, ConversationRecord, ConversationSourcesPayload } from '#lib/types.ts';

  let loading = true;
  let error = '';
  let actionMessage = '';
  let requeueing = false;
  let rebuilding = false;
  let requeueConfirmOpen = false;
  let detail: ConversationRecord | null = null;
  let memory: ConversationMemoryPayload | null = null;
  let sources: ConversationSourcesPayload | null = null;
  let lastTargetKey = '';

  onMount(async () => {
    await loadConversation();
  });

  $: targetKey =
    `${$selectedTarget?.id ?? 'none'}:${$page.params.id}:` +
    `${$page.url.searchParams.get('user_id') ?? ''}:` +
    `${$page.url.searchParams.get('generation_id') ?? ''}:` +
    `${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (targetKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = targetKey;
    void loadConversation();
  }

  async function loadConversation(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !$page.params.id) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      const userId = $page.url.searchParams.get('user_id') ?? undefined;
      const generationId = $page.url.searchParams.get('generation_id') ?? undefined;
      [detail, memory, sources] = await Promise.all([
        fetchConversation($selectedTarget, $page.params.id, userId, generationId),
        fetchConversationMemory($selectedTarget, $page.params.id, userId, generationId),
        fetchConversationSources($selectedTarget, $page.params.id, userId)
      ]);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load conversation detail';
    } finally {
      loading = false;
    }
  }

  async function requeueAnalysis(): Promise<void> {
    if (!$selectedTarget || !detail) {
      return;
    }
    requeueing = true;
    error = '';
    actionMessage = '';
    try {
      const userId = $page.url.searchParams.get('user_id') ?? detail.user_id;
      await requeueTask($selectedTarget, {
        conversation_id: detail.conversation_id,
        user_id: userId,
        task_type: 'analysis.extract'
      });
      actionMessage = 'Analysis task requeued on the selected target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to requeue analysis';
    } finally {
      requeueing = false;
    }
  }

  async function rebuildConversation(): Promise<void> {
    if (!$selectedTarget || !detail) {
      return;
    }
    rebuilding = true;
    error = '';
    actionMessage = '';
    try {
      const rebuild = await createMemoryRebuild($selectedTarget, {
        userId: detail.user_id,
        scope: 'conversation',
        conversationId: detail.conversation_id,
        activateOnSuccess: false
      });
      const generationId = rebuild.item.generation_id;
      if (generationId) {
        const nextUrl = new URL($page.url.href);
        nextUrl.searchParams.set('user_id', detail.user_id);
        nextUrl.searchParams.set('generation_id', generationId);
        await goto(nextUrl, { reset: false });
      } else {
        await loadConversation();
      }
      actionMessage = 'Conversation rebuild candidate created. Viewing the candidate generation while the worker replays the latest retained snapshot.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to create conversation rebuild';
    } finally {
      rebuilding = false;
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Conversation"
    title={$page.params.id}
    description="Inspect the message timeline, worker lifecycle metadata, and memory artifacts linked to this conversation."
  >
    <div slot="actions">
      <Button class="btn-secondary btn-sm" type="button" disabled={requeueing} onclick={() => (requeueConfirmOpen = true)}>
        {requeueing ? 'Requeueing…' : 'Requeue analysis'}
      </Button>
      <Button class="btn-secondary btn-sm" type="button" disabled={rebuilding || !sources?.has_snapshot} onclick={rebuildConversation}>
        {rebuilding ? 'Creating…' : 'Rebuild'}
      </Button>
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to open conversation detail pages.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading conversation detail…</div>
  {:else if detail}
    {#if actionMessage}
      <div class="notice-card is-success">{actionMessage}</div>
    {/if}

    <div class="conversation-grid">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Timeline</h2>
            <p class="panel-meta">Protocol and message flow on the wire.</p>
          </div>
          <div class="inline-group">
            <StatusBadge tone={detail.finalize_status === 'ended' ? 'success' : detail.finalize_status === 'failed' ? 'danger' : 'warning'} label={detail.finalize_status} />
            <StatusBadge tone={detail.summary_status === 'completed' ? 'success' : detail.summary_status === 'failed' ? 'danger' : detail.summary_status === 'skipped' ? 'muted' : 'warning'} label={`summary ${detail.summary_status}`} />
            <StatusBadge tone={detail.analysis_status === 'completed' ? 'success' : detail.analysis_status === 'failed' ? 'danger' : detail.analysis_status === 'skipped' ? 'muted' : 'warning'} label={`analysis ${detail.analysis_status}`} />
          </div>
        </div>
        <div class="chat-list">
          {#each detail.messages as message, index}
            <ChatMessageCard message={{ ...message, id: message.id ?? `${detail.conversation_id}-${index}` }} />
          {/each}
        </div>
        {#if (detail.parts?.length ?? 0) > 0}
          <div class="page-stack" style="margin-top: 12px;">
            <div class="page-header">
              <div class="page-header-meta">
                <h3 class="section-title" style="font-size: var(--font-size-md);">Materialized timeline</h3>
                <p class="panel-meta">Message parts and extracted artifacts.</p>
              </div>
            </div>
            <div class="stack-list">
              {#each detail.parts ?? [] as part}
                <div class="debug-block debug-block-muted">
                  <div class="inline-group" style="justify-content: space-between;">
                    <strong>{part.role} · {part.part_type}</strong>
                    <span class="helper-text mono">seq {part.sequence_num} · part {part.part_index}</span>
                  </div>
                  {#if part.source_url && part.part_type === 'image'}
                    <img src={part.source_url} alt={part.filename ?? 'image part'} style="max-width: 100%; border-radius: var(--radius-sm); margin-top: 8px;" />
                  {:else if part.source_url && part.part_type === 'audio'}
                    <audio controls style="width: 100%; margin-top: 8px;" src={part.source_url}></audio>
                  {:else if part.source_url && part.part_type === 'video'}
                    <!-- svelte-ignore a11y_media_has_caption -->
                    <video controls style="width: 100%; border-radius: var(--radius-sm); margin-top: 8px;" src={part.source_url}></video>
                  {:else if part.source_url && part.part_type === 'file'}
                    <a class="link-inline" href={part.source_url} target="_blank" rel="noreferrer">{part.filename ?? 'Open file'}</a>
                  {/if}
                  {#if part.text_content}
                    <div class="helper-text" style="margin-top: 8px;">{part.text_content}</div>
                  {/if}
                  {#if (detail.artifacts ?? []).filter((artifact) => artifact.part_id === part.part_id).length > 0}
                    <div class="stack-list" style="margin-top: 8px;">
                      {#each (detail.artifacts ?? []).filter((artifact) => artifact.part_id === part.part_id) as artifact}
                        <div class="debug-block">
                          <div class="inline-group" style="justify-content: space-between;">
                            <strong>{artifact.artifact_type}</strong>
                            <StatusBadge tone="muted" label={artifact.status} />
                          </div>
                          {#if artifact.text_content}
                            <div class="message-body">{artifact.text_content}</div>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </section>

      <aside class="conversation-side">
        <div class="card page-stack">
          <div class="page-header">
            <div class="page-header-meta">
              <h3 class="section-title">Envelope</h3>
              <p class="panel-meta">Routing &amp; protocol metadata.</p>
            </div>
          </div>
          <dl class="meta-grid compact">
            <div>
              <dt>Provider</dt>
              <dd class="mono">{detail.provider}</dd>
            </div>
            <div>
              <dt>Protocol</dt>
              <dd class="mono">{detail.request_format}</dd>
            </div>
            <div>
              <dt>Mode</dt>
              <dd class="mono">{detail.mode}</dd>
            </div>
            <div>
              <dt>Updated</dt>
              <dd>{new Date(detail.updated_at).toLocaleString()}</dd>
            </div>
          </dl>
        </div>

        <div class="card page-stack">
          <div class="page-header">
            <div class="page-header-meta">
              <h3 class="section-title">Snapshot</h3>
              <p class="panel-meta">Source retention for rebuilds.</p>
            </div>
          </div>
          {#if sources?.has_snapshot && sources.latest_snapshot}
            <dl class="meta-grid compact">
              <div>
                <dt>Version</dt>
                <dd class="mono">v{sources.latest_snapshot.snapshot_version}</dd>
              </div>
              <div>
                <dt>Hash</dt>
                <dd class="mono">{sources.latest_snapshot.source_hash}</dd>
              </div>
              <div>
                <dt>Captured</dt>
                <dd>{sources.latest_snapshot.updated_at ? new Date(sources.latest_snapshot.updated_at).toLocaleString() : 'Unknown'}</dd>
              </div>
            </dl>
          {:else}
            <div class="empty-state">No retained snapshot for this conversation.</div>
          {/if}
        </div>

        <div class="card page-stack">
          <div class="page-header">
            <div class="page-header-meta">
              <h3 class="section-title">Usage</h3>
              <p class="panel-meta">Token totals.</p>
            </div>
          </div>
          <JsonView value={detail.usage} />
        </div>

        <div class="card page-stack">
          <div class="page-header">
            <div class="page-header-meta">
              <h3 class="section-title">Metadata</h3>
              <p class="panel-meta">Stored request &amp; worker context.</p>
            </div>
          </div>
          <JsonView value={detail.metadata ?? {}} />
        </div>

        <div class="card page-stack">
          <div class="page-header">
            <div class="page-header-meta">
              <h3 class="section-title">Segments</h3>
              <p class="panel-meta">Incremental dialogue slices.</p>
            </div>
          </div>
          {#if (detail.segments?.length ?? 0) === 0}
            <div class="empty-state">No segments materialized yet.</div>
          {:else}
            <div class="stack-list">
              {#each detail.segments ?? [] as segment}
                <div class="debug-block debug-block-muted">
                  <div class="inline-group" style="justify-content: space-between;">
                    <div>
                      <strong>Segment #{segment.segment_index}</strong>
                      <div class="helper-text mono">seq {segment.start_sequence} → {segment.end_sequence}</div>
                    </div>
                    <div class="helper-text">
                      {segment.message_count} msgs
                      {#if segment.overlap_message_count > 0}
                        · overlap {segment.overlap_message_count}
                      {/if}
                    </div>
                  </div>
                  <div class="message-body">{segment.content}</div>
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <div class="card page-stack">
          <div class="page-header">
            <div class="page-header-meta">
              <h3 class="section-title">Memory output</h3>
              <p class="panel-meta">Documents linked to this conversation.</p>
            </div>
          </div>
          {#if memory?.generation_id}
            <div class="helper-text mono">Generation: {memory.generation_id}</div>
          {/if}
          {#if (memory?.documents.length ?? 0) === 0}
            <div class="empty-state">No persisted memory documents linked yet.</div>
          {:else}
            <div class="stack-list">
              {#each memory?.documents ?? [] as item}
                {@const routeKind = memoryRouteKindFromDocumentType(item.type)}
                <div class="debug-block debug-block-muted">
                  <div class="inline-group" style="justify-content: space-between;">
                    <div>
                      <strong>{item.type}</strong>
                      <div class="helper-text mono">{item.id}</div>
                    </div>
                    <div class="inline-group" style="gap: 4px;">
                      {#if routeKind}
                        <a class="btn-ghost btn-sm" href={memoryDetailHref(routeKind, item.id, detail.user_id)}>Open</a>
                        <a class="btn-ghost btn-sm" href={memoryCollectionHref(routeKind, detail.user_id)}>Workspace</a>
                      {/if}
                    </div>
                  </div>
                  <div class="message-body">{item.content}</div>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </aside>
    </div>
  {/if}
</section>

<ConfirmActionDialog
  bind:open={requeueConfirmOpen}
  title="Requeue conversation analysis?"
  description="A new analysis task will be published for this conversation and may update derived memory."
  subject={$page.params.id}
  confirmLabel="Requeue analysis"
  tone="primary"
  busy={requeueing}
  on:confirm={async () => {
    await requeueAnalysis();
    requeueConfirmOpen = false;
  }}
/>

<style>
  .conversation-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(320px, 1fr);
    gap: 14px;
    align-items: start;
  }

  .conversation-side {
    display: grid;
    gap: 14px;
    align-content: start;
    min-width: 0;
  }

  .meta-grid {
    display: grid;
    gap: 8px;
    margin: 0;
  }

  .meta-grid.compact {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .meta-grid > div {
    display: grid;
    gap: 2px;
    padding: 8px 10px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
  }

  .meta-grid dt {
    color: var(--text-muted);
    font-size: var(--font-size-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0;
    margin: 0;
  }

  .meta-grid dd {
    margin: 0;
    color: var(--text);
    font-size: var(--font-size-sm);
  }

  @media (max-width: 1100px) {
    .conversation-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
