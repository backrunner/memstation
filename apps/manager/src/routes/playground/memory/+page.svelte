<svelte:options runes={false} />

<script lang="ts">
  import { fetchConversation, fetchConversationMemory, searchAdminMemories } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import MemoryKeywordSignals from '$lib/components/MemoryKeywordSignals.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '$lib/memory/keyword-signals';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { ConversationMemoryPayload, ConversationRecord, SearchMemoriesResult } from '$lib/types';

  let conversationId = '';
  let searchQuery = '';
  let loading = false;
  let searchLoading = false;
  let error = '';
  let detail: ConversationRecord | null = null;
  let memory: ConversationMemoryPayload | null = null;
  let searchResult: SearchMemoriesResult | null = null;

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
  <div class="hero-card">
    <span class="sidebar-label">Playground / Memory</span>
    <h1 class="page-title">Validate extraction output and recall ranking around a conversation</h1>
    <p class="page-description">
      Inspect a conversation's persisted memory products, check current worker statuses, and run live recall queries against the selected target's memory engine.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect memory extraction details.</div>
  {:else}
    <section class="card">
      <div class="filters two-up">
        <label class="field">
          <span class="field-label">Conversation ID</span>
          <input class="input" bind:value={conversationId} placeholder="conv-..." />
        </label>
        <div class="field" style="align-self: end;">
          <button class="button-secondary" type="button" disabled={loading || !conversationId.trim()} on:click={inspectConversation}>{loading ? 'Loading...' : 'Inspect conversation'}</button>
        </div>
        <label class="field">
          <span class="field-label">Memory search query</span>
          <input class="input" bind:value={searchQuery} placeholder="recent preference, travel plan, project status" />
        </label>
        <div class="field" style="align-self: end;">
          <button class="button" type="button" disabled={searchLoading || !searchQuery.trim()} on:click={runSearch}>{searchLoading ? 'Searching...' : 'Run recall search'}</button>
        </div>
      </div>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {/if}

    <div class="grid-2" style="align-items: start;">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Extraction status</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Conversation-linked memory output</h2>
          </div>
          <div class="toolbar">
            <StatusBadge tone={detail?.summary_status === 'completed' ? 'success' : detail?.summary_status === 'failed' ? 'danger' : detail?.summary_status === 'skipped' ? 'muted' : 'warning'} label={`summary ${detail?.summary_status ?? 'n/a'}`} />
            <StatusBadge tone={detail?.analysis_status === 'completed' ? 'success' : detail?.analysis_status === 'failed' ? 'danger' : detail?.analysis_status === 'skipped' ? 'muted' : 'warning'} label={`analysis ${detail?.analysis_status ?? 'n/a'}`} />
          </div>
        </div>

        {#if !detail}
          <div class="empty-state">Inspect a conversation to see its memory chain.</div>
        {:else}
          <div class="toolbar">
            <div class="debug-block">
              <div class="sidebar-label">Conversation metadata</div>
              <JsonView value={detail.metadata ?? {}} />
            </div>
            {#if (memory?.documents.length ?? 0) === 0}
              <div class="empty-state">No persisted memory documents are linked to this conversation yet.</div>
            {:else}
              {#each memory?.documents ?? [] as item (item.id)}
                <article class="debug-block">
                  <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                    <strong>{item.type}</strong>
                    <StatusBadge tone={documentTone(item.type)} label={item.type} />
                  </div>
                  <div class="message-body">{item.content}</div>
                  <MemoryKeywordSignals
                    matchedKeywords={matchedKeywordsFromMetadata(item.metadata)}
                    sourceKeywords={sourceKeywordsFromMetadata(item.metadata)}
                  />
                  {#if item.metadata}
                    <div style="margin-top: 12px;">
                      <JsonView value={item.metadata} emptyLabel="No metadata" />
                    </div>
                  {/if}
                </article>
              {/each}
            {/if}
          </div>
        {/if}
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Recall search</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Current semantic ranking</h2>
          </div>
          {#if searchResult}
            <StatusBadge tone="brand" label={`${searchResult.total} matches`} />
          {/if}
        </div>

        {#if !searchResult}
          <div class="empty-state">Run a search to inspect scored memory recall and effective ranking order.</div>
        {:else}
          <div class="helper-text" style="margin-bottom: 12px;">Query embedding tokens: {searchResult.queryEmbeddingTokens}</div>
          <div class="toolbar">
            {#each searchResult.memories as item (item.id)}
              <article class="debug-block">
                <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                  <strong>{item.type}</strong>
                  <span class="subtle-text">semantic {item.score.toFixed(3)} · effective {item.effectiveScore.toFixed(3)}</span>
                </div>
                <div class="message-body">{item.content}</div>
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
