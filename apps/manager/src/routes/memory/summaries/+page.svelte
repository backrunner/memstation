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
  let summaries: MemoryDocument[] = [];
  let activeUserId: string | undefined;
  let routeKey = '';
  let visibleSummaries: MemoryDocument[] = [];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
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

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      summaries = await fetchAdminMemoryDocuments($selectedTarget, {
        kind: 'summary',
        userId: activeUserId,
        limit: 200
      });
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
  <div class="hero-card">
    <span class="sidebar-label">Memory / Summaries</span>
    <h1 class="page-title">Review and correct conversation summaries that seed memory recall</h1>
    <p class="page-description">Summaries are the primary rollup layer used to condense conversations into retrievable memory nodes and downstream keyword extraction.</p>
    {#if activeUserId}
      <div class="helper-text">Scoped to user `{activeUserId}`.</div>
    {/if}
  </div>

  <MemoryWorkspaceNav current="/memory/summaries" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect summary records.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading summary records...</div>
  {:else}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}
    <section class="card page-stack">
      <div class="filters two-up">
        <label class="field">
          <span class="field-label">Filter summaries</span>
          <input class="input" bind:value={filter} placeholder="topic label, incident, preference, project name..." />
        </label>
        <div class="field" style="align-self: end;">
          <div class="helper-text">{visibleSummaries.length} of {summaries.length} summaries visible</div>
        </div>
      </div>
      {#if visibleSummaries.length === 0}
        <div class="empty-state">No summaries match the current filter.</div>
      {:else}
        <div class="toolbar">
          {#each visibleSummaries as summary}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                <div>
                  <strong>{typeof summary.metadata?.topic_label === 'string' ? summary.metadata.topic_label : 'Summary'}</strong>
                  <div class="subtle-text">{summary.id}</div>
                </div>
                <div class="inline-group" style="gap: 8px;">
                  <StatusBadge tone="muted" label={summary.conversation_id ? 'conversation-linked' : 'manual'} />
                  <a class="button-ghost" href={memoryDetailHref('summaries', summary.id, activeUserId)}>Open</a>
                  {#if editingId === summary.id}
                    <button class="button-ghost" type="button" on:click={cancelEdit}>Cancel</button>
                    <button class="button" type="button" disabled={saving} on:click={() => saveEdit(summary)}>{saving ? 'Saving...' : 'Save'}</button>
                  {:else}
                    <button class="button-ghost" type="button" on:click={() => startEdit(summary)}>Edit</button>
                  {/if}
                </div>
              </div>
              {#if editingId === summary.id}
                <textarea class="textarea" rows="7" bind:value={draftContent}></textarea>
              {:else}
                <div class="message-body">{summary.content}</div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>
