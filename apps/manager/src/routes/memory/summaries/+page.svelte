<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import { fetchAdminMemoryDocuments, updateAdminMemoryDocument } from '#lib/api/client.ts';
  import MemoryWorkspaceNav from '#lib/components/MemoryWorkspaceNav.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import { memoryDetailHref } from '#lib/memory/routes.ts';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import TextareaField from '#lib/components/TextareaField.svelte';
  import {
    buildSessionDraftKey,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '#lib/stores/session-drafts.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { MemoryDocument } from '#lib/types.ts';

  type SummaryListDraft = {
    filter: string;
    editingId: string;
    draftContent: string;
  };

  const summariesDraftVersion = 1;

  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let filter = '';
  let lastTargetKey = '';
  let editingId = '';
  let draftContent = '';
  let summaries: MemoryDocument[] = [];
  let activeUserId: string | undefined;
  let routeKey = '';
  let visibleSummaries: MemoryDocument[] = [];
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let currentDraft = emptyDraft();

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: summariesDraftKey = buildSessionDraftKey('memory.summaries', $selectedTarget?.id, activeUserId ?? 'admin');
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
  }
  $: if (summariesDraftKey !== activeDraftKey) {
    activeDraftKey = summariesDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<SummaryListDraft>(summariesDraftKey, summariesDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: currentDraft = {
    filter,
    editingId,
    draftContent
  };
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, summariesDraftVersion, currentDraft);
  }

  onMount(async () => {
    await loadData();
  });

  $: visibleSummaries = summaries.filter((summary) => {
    const needle = filter.trim().toLowerCase();
    if (!needle) {
      return true;
    }
    const topic = typeof summary.metadata?.topic_label === 'string' ? summary.metadata.topic_label : '';
      return [topic, summary.content].join(' ').toLowerCase().includes(needle);
  });

  useSessionDraftUnloadWarning(
    () => {
      const activeSummary = summaries.find((item) => item.id === editingId);
      return Boolean(activeSummary && draftContent !== activeSummary.content);
    },
    'This summary edit has unsaved changes. Closing or reloading the page may discard the draft kept in this tab.'
  );

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, summariesDraftVersion, currentDraft);
    }
  });

  function emptyDraft(): SummaryListDraft {
    return {
      filter: '',
      editingId: '',
      draftContent: ''
    };
  }

  function applyDraft(draft: SummaryListDraft | null, nextSummaries: MemoryDocument[] = summaries): void {
    filter = draft?.filter ?? '';
    const requestedId = draft?.editingId ?? '';
    const activeSummary = requestedId ? nextSummaries.find((item) => item.id === requestedId) : null;
    editingId = activeSummary?.id ?? '';
    draftContent = activeSummary ? draft?.draftContent ?? activeSummary.content : '';
  }

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      draftReady = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      const nextSummaries = await fetchAdminMemoryDocuments($selectedTarget, {
        kind: 'summary',
        userId: activeUserId,
        limit: 200
      });
      summaries = nextSummaries;
      hydratingDraft = true;
      applyDraft(readSessionDraft<SummaryListDraft>(summariesDraftKey, summariesDraftVersion), nextSummaries);
      hydratingDraft = false;
      draftReady = true;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load memory summaries';
    } finally {
      loading = false;
    }
  }

  function startEdit(item: MemoryDocument): void {
    editingId = item.id;
    draftContent = item.content;
    success = '';
    error = '';
  }

  function cancelEdit(): void {
    editingId = '';
    draftContent = '';
  }

  async function saveEdit(item: MemoryDocument): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const updated = await updateAdminMemoryDocument($selectedTarget, 'summary', item.id, draftContent, activeUserId);
      summaries = summaries.map((entry) => (entry.id === updated.id ? updated : entry));
      editingId = '';
      draftContent = '';
      success = 'Summary updated and audited on the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to update summary';
    } finally {
      saving = false;
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Inspect / Memory"
    title="Summaries"
    description="Primary rollup layer that condenses conversations into retrievable memory nodes."
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  />

  <MemoryWorkspaceNav current="/memory/summaries" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect summary records.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading summary records…</div>
  {:else}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}
    <section class="card page-stack">
      <div class="filters two-up">
        <TextInputField
          label="Filter"
          bind:value={filter}
          placeholder="topic label, incident, preference, project name…"
        />
        <div class="field" style="align-self: end;">
          <div class="helper-text">{visibleSummaries.length} of {summaries.length} summaries visible</div>
        </div>
      </div>
      {#if visibleSummaries.length === 0}
        <div class="empty-state">No summaries match the current filter.</div>
      {:else}
        <div class="stack-list data-list">
          {#each visibleSummaries as summary}
            <article class="summary-card data-list-row">
              <header class="summary-head">
                <div class="summary-head-main">
                  <strong>{typeof summary.metadata?.topic_label === 'string' ? summary.metadata.topic_label : 'Summary'}</strong>
                  <span class="helper-text mono">{summary.id}</span>
                </div>
                <div class="inline-group">
                  <a class="btn-ghost btn-sm" href={memoryDetailHref('summaries', summary.id, activeUserId)}>Open</a>
                  {#if editingId === summary.id}
                    <Button class="btn-ghost btn-sm" type="button" onclick={cancelEdit}>Cancel</Button>
                    <Button class="btn-primary btn-sm" type="button" disabled={saving} onclick={() => saveEdit(summary)}>
                      {saving ? 'Saving…' : 'Save'}
                    </Button>
                  {:else}
                    <Button class="btn-secondary btn-sm" type="button" onclick={() => startEdit(summary)}>Edit</Button>
                  {/if}
                </div>
              </header>
              {#if editingId === summary.id}
                <TextareaField label="Summary content" bind:value={draftContent} rows={6} />
              {:else}
                <div class="message-body">{summary.content}</div>
              {/if}
            </article>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>

<style>
  .summary-card {
    display: grid;
    gap: 8px;
  }

  .summary-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  .summary-head-main {
    display: grid;
    gap: 2px;
  }
</style>
