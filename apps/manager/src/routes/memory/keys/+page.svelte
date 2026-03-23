<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import {
    createAdminKeyMemory,
    deleteAdminKeyMemory,
    fetchAdminKeyMemories,
    updateAdminKeyMemory
  } from '$lib/api/client';
  import JsonView from '$lib/components/JsonView.svelte';
  import MemoryWorkspaceNav from '$lib/components/MemoryWorkspaceNav.svelte';
  import { memoryDetailHref } from '$lib/memory/routes';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { KeyMemoryRecord } from '$lib/types';

  let loading = true;
  let saving = false;
  let creating = false;
  let deletingId = '';
  let error = '';
  let success = '';
  let filter = '';
  let dateFrom = '';
  let dateTo = '';
  let selectedTiers: string[] = [];
  let lastTargetKey = '';
  let editingId = '';
  let draftValue = '';
  let draftCategory = '';
  let draftReviewStatus = 'pending';
  let createKey = '';
  let createValue = '';
  let createCategory = '';
  let createReviewStatus = 'pending';
  let keyMemories: KeyMemoryRecord[] = [];
  let activeUserId: string | undefined;
  let routeKey = '';
  let visibleKeyMemories: KeyMemoryRecord[] = [];
  const timeTierOptions = [
    { value: 'active', label: 'Active' },
    { value: 'short_term', label: 'Short term' },
    { value: 'long_term', label: 'Long term' },
    { value: 'archived', label: 'Archived' }
  ];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
  }

  onMount(async () => {
    await loadData();
  });

  $: visibleKeyMemories = keyMemories;

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      keyMemories = await fetchAdminKeyMemories($selectedTarget, {
        userId: activeUserId,
        limit: 200,
        query: filter.trim(),
        dateFrom: startOfDayISO(dateFrom),
        dateTo: endOfDayISO(dateTo),
        timeTiers: selectedTiers
      });
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load key memories';
    } finally {
      loading = false;
    }
  }

  function startEdit(item: KeyMemoryRecord): void {
    editingId = item.id;
    draftValue = item.value;
    draftCategory = item.category ?? '';
    draftReviewStatus = item.review_status ?? 'pending';
    success = '';
    error = '';
  }

  function cancelEdit(): void {
    editingId = '';
    draftValue = '';
    draftCategory = '';
    draftReviewStatus = 'pending';
  }

  async function saveEdit(item: KeyMemoryRecord): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const updated = await updateAdminKeyMemory($selectedTarget, item.id, {
        userId: activeUserId,
        value: draftValue,
        category: draftCategory,
        reviewStatus: draftReviewStatus
      });
      keyMemories = keyMemories.map((entry) => (entry.id === updated.id ? updated : entry));
      cancelEdit();
      success = 'Key memory updated and audited on the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to update key memory';
    } finally {
      saving = false;
    }
  }

  async function createItem(): Promise<void> {
    if (!$selectedTarget || !createKey.trim() || !createValue.trim()) {
      return;
    }
    creating = true;
    error = '';
    success = '';
    try {
      const created = await createAdminKeyMemory($selectedTarget, {
        userId: activeUserId,
        key: createKey.trim(),
        value: createValue.trim(),
        category: createCategory.trim(),
        reviewStatus: createReviewStatus
      });
      keyMemories = [created, ...keyMemories];
      createKey = '';
      createValue = '';
      createCategory = '';
      createReviewStatus = 'pending';
      success = 'Key memory created and audited on the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to create key memory';
    } finally {
      creating = false;
    }
  }

  async function removeItem(item: KeyMemoryRecord): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    deletingId = item.id;
    error = '';
    success = '';
    try {
      await deleteAdminKeyMemory($selectedTarget, item.id, activeUserId);
      keyMemories = keyMemories.filter((entry) => entry.id !== item.id);
      if (editingId === item.id) {
        cancelEdit();
      }
      success = 'Key memory deleted and audited on the target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to delete key memory';
    } finally {
      deletingId = '';
    }
  }

  function toggleTier(value: string): void {
    if (selectedTiers.includes(value)) {
      selectedTiers = selectedTiers.filter((item) => item !== value);
      return;
    }
    selectedTiers = [...selectedTiers, value];
  }

  function startOfDayISO(value: string): string | undefined {
    if (!value) {
      return undefined;
    }
    return new Date(`${value}T00:00:00`).toISOString();
  }

  function endOfDayISO(value: string): string | undefined {
    if (!value) {
      return undefined;
    }
    return new Date(`${value}T23:59:59.999`).toISOString();
  }

  function dateTimeLabel(value?: string): string {
    if (!value) {
      return 'n/a';
    }
    return new Date(value).toLocaleString();
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Memory / Key Memories</span>
    <h1 class="page-title">Manage stable key-value memory slots and tool-facing state</h1>
    <p class="page-description">These records back whitelisted memory tools and admin workflows for durable slots such as preferences, recent conversation markers, and operator-curated notes.</p>
    {#if activeUserId}
      <div class="helper-text">Scoped to user `{activeUserId}`.</div>
    {/if}
  </div>

  <MemoryWorkspaceNav current="/memory/keys" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect key memories.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading key memories...</div>
  {:else}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}

    <section class="card page-stack">
      <div class="page-header">
        <div>
          <div class="sidebar-label">Create key memory</div>
          <h2 class="page-title" style="font-size: 1.35rem;">Write an operator-managed stable memory slot</h2>
        </div>
      </div>
      <div class="form-grid two-up">
        <label class="field">
          <span class="field-label">Key</span>
          <input class="input" bind:value={createKey} placeholder="preferred_name or recent_conversation" />
        </label>
        <label class="field">
          <span class="field-label">Category</span>
          <input class="input" bind:value={createCategory} placeholder="profile, workflow, system" />
        </label>
        <label class="field" style="grid-column: 1 / -1;">
          <span class="field-label">Value</span>
          <textarea class="textarea" rows="4" bind:value={createValue}></textarea>
        </label>
        <label class="field">
          <span class="field-label">Review status</span>
          <select class="input" bind:value={createReviewStatus}>
            <option value="pending">pending</option>
            <option value="approved">approved</option>
            <option value="rejected">rejected</option>
          </select>
        </label>
      </div>
      <div class="inline-group" style="justify-content: flex-end;">
        <button class="button" type="button" disabled={creating || !createKey.trim() || !createValue.trim()} on:click={createItem}>{creating ? 'Creating...' : 'Create key memory'}</button>
      </div>
    </section>

    <section class="card page-stack">
      <div class="filters two-up">
        <label class="field">
          <span class="field-label">Filter key memories</span>
          <input class="input" bind:value={filter} placeholder="key, category, value, review status..." />
        </label>
        <div class="field" style="align-self: end;">
          <div class="inline-group" style="justify-content: flex-end;">
            <div class="helper-text">{visibleKeyMemories.length} key memories returned</div>
            <button class="button-secondary" type="button" on:click={loadData}>Apply filters</button>
          </div>
        </div>
      </div>
      <div class="filters two-up">
        <label class="field">
          <span class="field-label">Date from</span>
          <input class="input" type="date" bind:value={dateFrom} />
        </label>
        <label class="field">
          <span class="field-label">Date to</span>
          <input class="input" type="date" bind:value={dateTo} />
        </label>
      </div>
      <div class="inline-group" style="flex-wrap: wrap;">
        {#each timeTierOptions as option}
          <button
            class={selectedTiers.includes(option.value) ? 'button-secondary' : 'button-ghost'}
            type="button"
            on:click={() => toggleTier(option.value)}
          >
            {option.label}
          </button>
        {/each}
      </div>
      {#if visibleKeyMemories.length === 0}
        <div class="empty-state">No key memories match the current filter.</div>
      {:else}
        <div class="toolbar">
          {#each visibleKeyMemories as keyMemory}
            <div class="debug-block">
              <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px; align-items: flex-start;">
                <div>
                  <strong>{keyMemory.key}</strong>
                  <div class="subtle-text">{keyMemory.id}</div>
                </div>
                <div class="inline-group" style="gap: 8px; flex-wrap: wrap; justify-content: flex-end;">
                  {#if keyMemory.time_tier}
                    <StatusBadge tone="muted" label={keyMemory.time_tier} />
                  {/if}
                  <StatusBadge tone="muted" label={keyMemory.category ?? 'uncategorized'} />
                  <StatusBadge tone={keyMemory.review_status === 'approved' ? 'success' : keyMemory.review_status === 'rejected' ? 'danger' : 'warning'} label={keyMemory.review_status ?? 'pending'} />
                  {#if keyMemory.effective_score !== undefined}
                    <StatusBadge tone="brand" label={`effective ${keyMemory.effective_score.toFixed(3)}`} />
                  {/if}
                  <a class="button-ghost" href={memoryDetailHref('keys', keyMemory.id, activeUserId)}>Open</a>
                  {#if editingId === keyMemory.id}
                    <button class="button-ghost" type="button" on:click={cancelEdit}>Cancel</button>
                    <button class="button" type="button" disabled={saving} on:click={() => saveEdit(keyMemory)}>{saving ? 'Saving...' : 'Save'}</button>
                  {:else}
                    <button class="button-ghost" type="button" on:click={() => startEdit(keyMemory)}>Edit</button>
                  {/if}
                  <button class="button-danger" type="button" disabled={deletingId === keyMemory.id} on:click={() => removeItem(keyMemory)}>{deletingId === keyMemory.id ? 'Deleting...' : 'Delete'}</button>
                </div>
              </div>

              {#if editingId === keyMemory.id}
                <div class="form-grid two-up">
                  <label class="field" style="grid-column: 1 / -1;">
                    <span class="field-label">Value</span>
                    <textarea class="textarea" rows="4" bind:value={draftValue}></textarea>
                  </label>
                  <label class="field">
                    <span class="field-label">Category</span>
                    <input class="input" bind:value={draftCategory} />
                  </label>
                  <label class="field">
                    <span class="field-label">Review status</span>
                    <select class="input" bind:value={draftReviewStatus}>
                      <option value="pending">pending</option>
                      <option value="approved">approved</option>
                      <option value="rejected">rejected</option>
                    </select>
                  </label>
                </div>
              {:else}
                <div class="helper-text" style="margin-bottom: 10px;">
                  activity {dateTimeLabel(keyMemory.activity_at)} · importance {(keyMemory.importance ?? 0).toFixed(3)} · decay {(keyMemory.decay_factor ?? 0).toFixed(3)}
                </div>
                <JsonView value={keyMemory} />
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>
