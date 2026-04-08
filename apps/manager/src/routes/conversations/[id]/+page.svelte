<svelte:options runes={false} />

<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { createMemoryRebuild, fetchConversation, fetchConversationMemory, fetchConversationSources, requeueTask } from '$lib/api/client';
  import ChatMessageCard from '$lib/components/ChatMessageCard.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import { memoryDetailHref } from '$lib/memory/routes';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { ConversationMemoryPayload, ConversationRecord, ConversationSourcesPayload } from '$lib/types';

  let loading = true;
  let error = '';
  let actionMessage = '';
  let requeueing = false;
  let rebuilding = false;
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
        const nextUrl = new URL($page.url);
        nextUrl.searchParams.set('user_id', detail.user_id);
        nextUrl.searchParams.set('generation_id', generationId);
        await goto(nextUrl, { keepFocus: true, noScroll: true });
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
  <div class="hero-card">
    <span class="sidebar-label">Conversation Detail</span>
    <h1 class="page-title">{$page.params.id}</h1>
    <p class="page-description">
      Inspect the exact message timeline, worker lifecycle metadata, and the memory artifacts linked to this conversation.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to open conversation detail pages.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading conversation detail...</div>
  {:else if detail}
    {#if actionMessage}
      <div class="notice-card">{actionMessage}</div>
    {/if}
    <div class="grid-2" style="align-items: start;">
      <section class="chat-thread-card card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Timeline</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Protocol and message flow</h2>
          </div>
          <div class="toolbar">
            <button class="button-ghost" type="button" disabled={requeueing} on:click={requeueAnalysis}>{requeueing ? 'Requeueing...' : 'Requeue analysis'}</button>
            <button class="button-ghost" type="button" disabled={rebuilding || !sources?.has_snapshot} on:click={rebuildConversation}>
              {rebuilding ? 'Creating rebuild...' : 'Rebuild this conversation'}
            </button>
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
      </section>

      <section class="debug-column">
        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Meta</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Conversation envelope</h2>
            </div>
          </div>
          <div class="toolbar">
            <div class="inline-group"><strong>Provider</strong><span>{detail.provider}</span></div>
            <div class="inline-group"><strong>Protocol</strong><span>{detail.request_format}</span></div>
            <div class="inline-group"><strong>Mode</strong><span>{detail.mode}</span></div>
            <div class="inline-group"><strong>Updated</strong><span>{new Date(detail.updated_at).toLocaleString()}</span></div>
          </div>
        </div>

        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Source retention</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Snapshot availability</h2>
            </div>
          </div>
          {#if sources?.has_snapshot && sources.latest_snapshot}
            <div class="toolbar">
              <div class="inline-group"><strong>Latest version</strong><span>v{sources.latest_snapshot.snapshot_version}</span></div>
              <div class="inline-group"><strong>Hash</strong><span>{sources.latest_snapshot.source_hash}</span></div>
              <div class="inline-group"><strong>Captured</strong><span>{sources.latest_snapshot.updated_at ? new Date(sources.latest_snapshot.updated_at).toLocaleString() : 'Unknown'}</span></div>
            </div>
          {:else}
            <div class="empty-state">No retained source snapshot exists for this conversation yet, so one-click rebuild is unavailable.</div>
          {/if}
        </div>

        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Usage</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Token totals</h2>
            </div>
          </div>
          <JsonView value={detail.usage} />
        </div>

        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Metadata</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Stored request and worker context</h2>
            </div>
          </div>
          <JsonView value={detail.metadata ?? {}} />
        </div>

        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Segments</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Incremental dialogue slices</h2>
            </div>
          </div>
          {#if (detail.segments?.length ?? 0) === 0}
            <div class="empty-state">No conversation segments have been materialized for this conversation yet.</div>
          {:else}
            <div class="toolbar" style="display: grid; gap: 12px;">
              {#each detail.segments ?? [] as segment}
                <div class="debug-block" style="background: var(--bg-panel-muted);">
                  <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                    <div>
                      <strong>Segment #{segment.segment_index}</strong>
                      <div class="subtle-text">seq {segment.start_sequence} -> {segment.end_sequence}</div>
                    </div>
                    <div class="subtle-text">
                      {segment.message_count} msgs
                      {#if segment.overlap_message_count > 0}
                        · overlap {segment.overlap_message_count} from seq {segment.overlap_from_sequence}
                      {/if}
                    </div>
                  </div>
                  <div class="message-body">{segment.content}</div>
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <div class="debug-block">
          <div class="page-header">
            <div>
              <div class="sidebar-label">Memory output</div>
              <h2 class="page-title" style="font-size: 1.2rem;">Documents linked to this conversation</h2>
            </div>
          </div>
          {#if memory?.generation_id}
            <div class="helper-text" style="margin-bottom: 10px;">Viewing generation `{memory.generation_id}`.</div>
          {/if}
          {#if (memory?.documents.length ?? 0) === 0}
            <div class="empty-state">No persisted memory documents are linked to this conversation yet.</div>
          {:else}
            <div class="toolbar">
              {#each memory?.documents ?? [] as item}
                <div class="debug-block" style="background: var(--bg-panel-muted);">
                  <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                    <div>
                      <strong>{item.type}</strong>
                      <div class="subtle-text">{item.id}</div>
                    </div>
                    <div class="inline-group" style="gap: 8px;">
                      <a class="button-ghost" href={memoryDetailHref(item.type === 'summary' ? 'summaries' : item.type === 'impression' ? 'impressions' : 'facts', item.id, detail.user_id)}>Open detail</a>
                      <a class="button-ghost" href={`/memory/${item.type === 'summary' ? 'summaries' : item.type === 'impression' ? 'impressions' : 'facts'}?user_id=${detail.user_id}`}>Open workspace</a>
                    </div>
                  </div>
                  <div class="message-body">{item.content}</div>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
</section>
