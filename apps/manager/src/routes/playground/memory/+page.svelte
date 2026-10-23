<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';

  import { fetchConversation, fetchConversationMemory, searchAdminMemories } from '#lib/api/client.ts';
  import JsonView from '#lib/components/JsonView.svelte';
  import MemoryKeywordSignals from '#lib/components/MemoryKeywordSignals.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '#lib/memory/keyword-signals.ts';
  import {
    buildSessionDraftKey,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '#lib/stores/session-drafts.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { ConversationMemoryPayload, ConversationRecord, SearchMemoriesResult } from '#lib/types.ts';

  type MemoryPlaygroundDraft = {
    conversationId: string;
    searchQuery: string;
    detail: ConversationRecord | null;
    memory: ConversationMemoryPayload | null;
    searchResult: SearchMemoriesResult | null;
  };

  const memoryDraftVersion = 1;

  let conversationId = '';
  let searchQuery = '';
  let loading = false;
  let searchLoading = false;
  let error = '';
  let detail: ConversationRecord | null = null;
  let memory: ConversationMemoryPayload | null = null;
  let searchResult: SearchMemoriesResult | null = null;
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let memoryDraftSnapshot: MemoryPlaygroundDraft = captureDraft();

  $: memoryDraftKey = buildSessionDraftKey('playground.memory', $selectedTarget?.id);
  $: if (memoryDraftKey !== activeDraftKey) {
    activeDraftKey = memoryDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<MemoryPlaygroundDraft>(memoryDraftKey, memoryDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: memoryDraftSnapshot = {
    conversationId,
    searchQuery,
    detail,
    memory,
    searchResult
  };
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, memoryDraftVersion, memoryDraftSnapshot);
  }

  useSessionDraftUnloadWarning(
    () => Boolean(conversationId.trim() || searchQuery.trim() || detail || memory || searchResult),
    'This memory playground session is only saved in the current tab. Closing or reloading the page will discard it.'
  );

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, memoryDraftVersion, captureDraft());
    }
  });

  function applyDraft(draft: MemoryPlaygroundDraft | null): void {
    conversationId = draft?.conversationId ?? '';
    searchQuery = draft?.searchQuery ?? '';
    detail = draft?.detail ?? null;
    memory = draft?.memory ?? null;
    searchResult = draft?.searchResult ?? null;
    error = '';
    loading = false;
    searchLoading = false;
  }

  function captureDraft(): MemoryPlaygroundDraft {
    return {
      conversationId,
      searchQuery,
      detail,
      memory,
      searchResult
    };
  }

  async function inspectConversation(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !conversationId.trim()) {
      return;
    }
    loading = true;
    error = '';
    try {
      [detail, memory] = await Promise.all([
        fetchConversation($selectedTarget, conversationId.trim()),
        fetchConversationMemory($selectedTarget, conversationId.trim())
      ]);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to inspect conversation memory';
    } finally {
      loading = false;
    }
  }

  async function runSearch(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !searchQuery.trim()) {
      return;
    }
    searchLoading = true;
    error = '';
    try {
      searchResult = await searchAdminMemories($selectedTarget, { query: searchQuery.trim(), topN: 12 });
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to search memory';
    } finally {
      searchLoading = false;
    }
  }

  function documentTone(type: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    switch (type) {
      case 'summary':
        return 'brand';
      case 'fact':
        return 'success';
      case 'impression':
        return 'warning';
      case 'key_memory':
        return 'danger';
      default:
        return 'muted';
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Playground"
    title="Recall"
    description="Inspect persisted extraction and run direct recall queries against the selected target."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect memory extraction details.</div>
  {:else}
    <div class="page-columns">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Inspect conversation</h2>
            <p class="panel-meta">Load metadata, messages, and persisted memory by id.</p>
          </div>
          <Button class="btn-secondary btn-sm" type="button" disabled={loading || !conversationId.trim()} onclick={inspectConversation}>
            {loading ? 'Loading…' : 'Inspect'}
          </Button>
        </div>
        <TextInputField label="Conversation ID" bind:value={conversationId} placeholder="conv-…" inputClass="input-mono" />
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Recall search</h2>
            <p class="panel-meta">Run a semantic query against persisted memory.</p>
          </div>
          <Button class="btn-primary btn-sm" type="button" disabled={searchLoading || !searchQuery.trim()} onclick={runSearch}>
            {searchLoading ? 'Searching…' : 'Search'}
          </Button>
        </div>
        <TextInputField
          label="Query"
          bind:value={searchQuery}
          placeholder="recent preference, travel plan, project status"
        />
      </section>
    </div>

    {#if error}
      <div class="error-state">{error}</div>
    {/if}

    <div class="page-columns">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Extraction</h2>
            <p class="panel-meta">Memory documents linked to the inspected conversation.</p>
          </div>
          <div class="inline-group">
            <StatusBadge tone={detail?.summary_status === 'completed' ? 'success' : detail?.summary_status === 'failed' ? 'danger' : detail?.summary_status === 'skipped' ? 'muted' : 'warning'} label={`summary ${detail?.summary_status ?? 'n/a'}`} />
            <StatusBadge tone={detail?.analysis_status === 'completed' ? 'success' : detail?.analysis_status === 'failed' ? 'danger' : detail?.analysis_status === 'skipped' ? 'muted' : 'warning'} label={`analysis ${detail?.analysis_status ?? 'n/a'}`} />
          </div>
        </div>

        {#if !detail}
          <div class="empty-state">Inspect a conversation to load memory.</div>
        {:else}
          <div class="stack-list">
            <CollapsibleSection title="Conversation metadata">
                <div style="margin-top: 8px;">
                  <JsonView value={detail.metadata ?? {}} />
                </div>
              </CollapsibleSection>
            {#if (memory?.documents.length ?? 0) === 0}
              <div class="empty-state">No persisted memory documents are linked to this conversation yet.</div>
            {:else}
              {#each memory?.documents ?? [] as item (item.id)}
                <article class="debug-block debug-block-muted">
                  <div class="inline-group" style="justify-content: space-between;">
                    <strong>{item.type}</strong>
                    <StatusBadge tone={documentTone(item.type)} label={item.type} />
                  </div>
                  <div class="message-body" style="margin-top: 6px;">{item.content}</div>
                  <MemoryKeywordSignals
                    matchedKeywords={matchedKeywordsFromMetadata(item.metadata)}
                    sourceKeywords={sourceKeywordsFromMetadata(item.metadata)}
                  />
                  {#if item.metadata}
                    <div style="margin-top: 8px;">
                      <JsonView value={item.metadata} emptyLabel="No metadata" />
                    </div>
                  {/if}
                </article>
              {/each}
            {/if}
          </div>
        {/if}
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Search results</h2>
            <p class="panel-meta">Top results ranked by effective score.</p>
          </div>
          {#if searchResult}
            <StatusBadge tone="brand" label={`${searchResult.total} matches`} />
          {/if}
        </div>

        {#if !searchResult}
          <div class="empty-state">Run a search to load recall results.</div>
        {:else}
          <div class="helper-text mono">Query embedding tokens: {searchResult.queryEmbeddingTokens}</div>
          <div class="stack-list">
            {#each searchResult.memories as item (item.id)}
              <article class="debug-block debug-block-muted">
                <div class="inline-group" style="justify-content: space-between;">
                  <strong>{item.type}</strong>
                  <span class="helper-text mono">semantic {item.score.toFixed(3)} · effective {item.effectiveScore.toFixed(3)}</span>
                </div>
                <div class="message-body" style="margin-top: 6px;">{item.content}</div>
                <MemoryKeywordSignals
                  matchedKeywords={matchedKeywordsFromMetadata(item.metadata)}
                  sourceKeywords={sourceKeywordsFromMetadata(item.metadata)}
                />
              </article>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</section>
