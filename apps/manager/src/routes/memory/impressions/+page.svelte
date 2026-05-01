<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { fetchAdminMemoryDocuments, updateAdminMemoryDocument } from '$lib/api/client';
  import MemoryWorkspaceNav from '$lib/components/MemoryWorkspaceNav.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { memoryDetailHref } from '$lib/memory/routes';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import TextareaField from '$lib/components/TextareaField.svelte';
  import {
    buildSessionDraftKey,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '$lib/stores/session-drafts';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { MemoryDocument } from '$lib/types';

  type ImpressionsListDraft = {
    filter: string;
    editingId: string;
    draftContent: string;
  };

  const impressionsDraftVersion = 1;

  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let filter = '';
  let lastTargetKey = '';
  let editingId = '';
  let draftContent = '';
  let impressions: MemoryDocument[] = [];
  let activeUserId: string | undefined;
  let routeKey = '';
  let visibleImpressions: MemoryDocument[] = [];
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let currentDraft = emptyDraft();

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: impressionsDraftKey = buildSessionDraftKey('memory.impressions', $selectedTarget?.id, activeUserId ?? 'admin');
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
  }
  $: if (impressionsDraftKey !== activeDraftKey) {
    activeDraftKey = impressionsDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<ImpressionsListDraft>(impressionsDraftKey, impressionsDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: currentDraft = {
    filter,
    editingId,
    draftContent
  };
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, impressionsDraftVersion, currentDraft);
  }

  onMount(async () => {
    await loadData();
  });

  $: visibleImpressions = impressions.filter((impression) => {
    const needle = filter.trim().toLowerCase();
    if (!needle) {
      return true;
    }
    return impression.content.toLowerCase().includes(needle);
  });

  useSessionDraftUnloadWarning(
    () => {
      const activeImpression = impressions.find((item) => item.id === editingId);
      return Boolean(activeImpression && draftContent !== activeImpression.content);
    },
    'This impression edit has unsaved changes. Closing or reloading the page may discard the draft kept in this tab.'
  );

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, impressionsDraftVersion, currentDraft);
    }
  });

  function emptyDraft(): ImpressionsListDraft {
    return {
      filter: '',
      editingId: '',
      draftContent: ''
    };
  }

  function applyDraft(draft: ImpressionsListDraft | null, nextImpressions: MemoryDocument[] = impressions): void {
    filter = draft?.filter ?? '';
    const requestedId = draft?.editingId ?? '';
    const activeImpression = requestedId ? nextImpressions.find((item) => item.id === requestedId) : null;
    editingId = activeImpression?.id ?? '';
    draftContent = activeImpression ? draft?.draftContent ?? activeImpression.content : '';
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
      const nextImpressions = await fetchAdminMemoryDocuments($selectedTarget, {
        kind: 'impression',
        userId: activeUserId,
        limit: 200
      });
      impressions = nextImpressions;
      hydratingDraft = true;
      applyDraft(readSessionDraft<ImpressionsListDraft>(impressionsDraftKey, impressionsDraftVersion), nextImpressions);
      hydratingDraft = false;
      draftReady = true;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load impressions';
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
      const updated = await updateAdminMemoryDocument($selectedTarget, 'impression', item.id, draftContent, activeUserId);
      impressions = impressions.map((entry) => (entry.id === updated.id ? updated : entry));
      editingId = '';
      draftContent = '';
      success = 'Impression updated and audited on the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to update impression';
    } finally {
      saving = false;
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Inspect / Memory"
    title="Impressions"
    description="Preference hints, attitudes, and familiarity signals useful for style adaptation and operator review."
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  />

  <MemoryWorkspaceNav current="/memory/impressions" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect impression records.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading impression records…</div>
  {:else}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}
    <section class="card page-stack">
      <div class="filters two-up">
        <TextInputField
          label="Filter"
          bind:value={filter}
          placeholder="tone, preference, attitude, repeated behavior…"
        />
        <div class="field" style="align-self: end;">
          <div class="helper-text">{visibleImpressions.length} of {impressions.length} impressions visible</div>
        </div>
      </div>
      {#if visibleImpressions.length === 0}
        <div class="empty-state">No impressions match the current filter.</div>
      {:else}
        <div class="stack-list">
          {#each visibleImpressions as impression}
            <article class="summary-card">
              <header class="summary-head">
                <div class="summary-head-main">
                  <strong>Impression</strong>
                  <span class="helper-text mono">{impression.id}</span>
                </div>
                <div class="inline-group">
                  <StatusBadge tone="muted" label={impression.conversation_id ? 'conversation-linked' : 'manual'} />
                  <a class="btn-ghost btn-sm" href={memoryDetailHref('impressions', impression.id, activeUserId)}>Open</a>
                  {#if editingId === impression.id}
                    <button class="btn-ghost btn-sm" type="button" on:click={cancelEdit}>Cancel</button>
                    <button class="btn-primary btn-sm" type="button" disabled={saving} on:click={() => saveEdit(impression)}>
                      {saving ? 'Saving…' : 'Save'}
                    </button>
                  {:else}
                    <button class="btn-secondary btn-sm" type="button" on:click={() => startEdit(impression)}>Edit</button>
                  {/if}
                </div>
              </header>
              {#if editingId === impression.id}
                <TextareaField label="Impression content" bind:value={draftContent} rows={5} />
              {:else}
                <div class="message-body">{impression.content}</div>
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
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
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
