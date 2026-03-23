<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import { fetchAdminMemoryDocuments, updateAdminMemoryDocument } from '$lib/api/client';
  import MemoryWorkspaceNav from '$lib/components/MemoryWorkspaceNav.svelte';
  import { memoryDetailHref } from '$lib/memory/routes';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { MemoryDocument } from '$lib/types';

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

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
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

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      facts = await fetchAdminMemoryDocuments($selectedTarget, {
        kind: 'fact',
        userId: activeUserId,
        limit: 200
      });
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
    <h1 class="page-title">Browse and correct factual memory statements</h1>
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
        <label class="field">
          <span class="field-label">Filter facts</span>
          <input class="input" bind:value={filter} placeholder="location, preference, schedule, identity, status..." />
        </label>
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
                <textarea class="textarea" rows="6" bind:value={draftContent}></textarea>
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
