<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { fetchAdminMemoryDocuments, updateAdminMemoryDocument } from '$lib/api/client';
  import MemoryWorkspaceNav from '$lib/components/MemoryWorkspaceNav.svelte';
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

  type FactsListDraft = {
    filter: string;
    editingId: string;
    draftContent: string;
  };

  const factsDraftVersion = 1;

  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let filter = '';
  let lastTargetKey = '';
  let editingId = '';
  let draftContent = '';
  let facts: MemoryDocument[] = [];
  let activeUserId: string | undefined;
  let routeKey = '';
  let visibleFacts: MemoryDocument[] = [];
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let currentDraft = emptyDraft();

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: factsDraftKey = buildSessionDraftKey('memory.facts', $selectedTarget?.id, activeUserId ?? 'admin');
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
  }
  $: if (factsDraftKey !== activeDraftKey) {
    activeDraftKey = factsDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<FactsListDraft>(factsDraftKey, factsDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: currentDraft = {
    filter,
    editingId,
    draftContent
  };
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, factsDraftVersion, currentDraft);
  }

  onMount(async () => {
    await loadData();
  });

  $: visibleFacts = facts.filter((fact) => {
    const needle = filter.trim().toLowerCase();
    if (!needle) {
      return true;
    }
    return fact.content.toLowerCase().includes(needle);
  });

  useSessionDraftUnloadWarning(
    () => {
      const activeFact = facts.find((item) => item.id === editingId);
      return Boolean(activeFact && draftContent !== activeFact.content);
    },
    'This fact edit has unsaved changes. Closing or reloading the page may discard the draft kept in this tab.'
  );

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, factsDraftVersion, currentDraft);
    }
  });

  function emptyDraft(): FactsListDraft {
    return {
      filter: '',
      editingId: '',
      draftContent: ''
    };
  }

  function applyDraft(draft: FactsListDraft | null, nextFacts: MemoryDocument[] = facts): void {
    filter = draft?.filter ?? '';
    const requestedId = draft?.editingId ?? '';
    const activeFact = requestedId ? nextFacts.find((item) => item.id === requestedId) : null;
    editingId = activeFact?.id ?? '';
    draftContent = activeFact ? draft?.draftContent ?? activeFact.content : '';
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
      const nextFacts = await fetchAdminMemoryDocuments($selectedTarget, {
        kind: 'fact',
        userId: activeUserId,
        limit: 200
      });
      facts = nextFacts;
      hydratingDraft = true;
      applyDraft(readSessionDraft<FactsListDraft>(factsDraftKey, factsDraftVersion), nextFacts);
      hydratingDraft = false;
      draftReady = true;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load facts';
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
      const updated = await updateAdminMemoryDocument($selectedTarget, 'fact', item.id, draftContent, activeUserId);
      facts = facts.map((entry) => (entry.id === updated.id ? updated : entry));
      editingId = '';
      draftContent = '';
      success = 'Fact updated and audited on the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to update fact';
    } finally {
      saving = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Memory / Facts</span>
    <h1 class="page-title">Facts</h1>
    <p class="page-description">Facts are structured statements that can supersede earlier information and feed both memory injection and admin correction workflows.</p>
    {#if activeUserId}
      <div class="helper-text">Scoped to user `{activeUserId}`.</div>
    {/if}
  </div>

  <MemoryWorkspaceNav current="/memory/facts" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect fact records.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading fact records...</div>
  {:else}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}
    <section class="card page-stack">
      <div class="filters two-up">
        <TextInputField label="Filter Facts" bind:value={filter} placeholder="location, preference, schedule, identity, status..." />
        <div class="field" style="align-self: end;">
          <div class="helper-text">{visibleFacts.length} of {facts.length} facts visible</div>
        </div>
      </div>
      {#if visibleFacts.length === 0}
        <div class="empty-state">No facts match the current filter.</div>
      {:else}
        <div class="toolbar">
          {#each visibleFacts as fact}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <div>
                  <strong>Fact</strong>
                  <div class="subtle-text">{fact.id}</div>
                </div>
                <div class="inline-group" style="gap: 8px;">
                  <StatusBadge tone="muted" label={fact.conversation_id ? 'conversation-linked' : 'manual'} />
                  <a class="button-ghost" href={memoryDetailHref('facts', fact.id, activeUserId)}>Open</a>
                  {#if editingId === fact.id}
                    <button class="button-ghost" type="button" on:click={cancelEdit}>Cancel</button>
                    <button class="button" type="button" disabled={saving} on:click={() => saveEdit(fact)}>{saving ? 'Saving...' : 'Save'}</button>
                  {:else}
                    <button class="button-ghost" type="button" on:click={() => startEdit(fact)}>Edit</button>
                  {/if}
                </div>
              </div>
              {#if editingId === fact.id}
                <TextareaField label="Fact Content" bind:value={draftContent} rows={6} />
              {:else}
                <div class="message-body">{fact.content}</div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>
