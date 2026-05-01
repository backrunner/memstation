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
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { memoryDetailHref } from '$lib/memory/routes';
  import SelectField from '$lib/components/SelectField.svelte';
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
  import type { KeyMemoryRecord } from '$lib/types';

  type KeyMemoriesDraft = {
    filter: string;
    dateFrom: string;
    dateTo: string;
    selectedTiers: string[];
    editingId: string;
    draftValue: string;
    draftCategory: string;
    draftReviewStatus: string;
    createKey: string;
    createValue: string;
    createCategory: string;
    createReviewStatus: string;
  };

  const keyMemoriesDraftVersion = 1;

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
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let currentDraft = emptyDraft();
  const timeTierOptions = [
    { value: 'active', label: 'Active' },
    { value: 'short_term', label: 'Short term' },
    { value: 'long_term', label: 'Long term' },
    { value: 'archived', label: 'Archived' }
  ];
  const reviewStatusItems = [
    { value: 'pending', label: 'pending' },
    { value: 'approved', label: 'approved' },
    { value: 'rejected', label: 'rejected' }
  ];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: keyMemoriesDraftKey = buildSessionDraftKey('memory.keys', $selectedTarget?.id, activeUserId ?? 'admin');
  $: if (keyMemoriesDraftKey !== activeDraftKey) {
    activeDraftKey = keyMemoriesDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<KeyMemoriesDraft>(keyMemoriesDraftKey, keyMemoriesDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadData();
  }
  $: currentDraft = {
    filter,
    dateFrom,
    dateTo,
    selectedTiers,
    editingId,
    draftValue,
    draftCategory,
    draftReviewStatus,
    createKey,
    createValue,
    createCategory,
    createReviewStatus
  };
  $: if (draftReady && !hydratingDraft && activeDraftKey) {
    writeSessionDraft(activeDraftKey, keyMemoriesDraftVersion, currentDraft);
  }

  onMount(async () => {
    await loadData();
  });

  $: visibleKeyMemories = keyMemories;

  useSessionDraftUnloadWarning(
    () => {
      const activeKeyMemory = keyMemories.find((item) => item.id === editingId);
      const editingDirty = Boolean(
        activeKeyMemory && (
          draftValue !== activeKeyMemory.value ||
          draftCategory !== (activeKeyMemory.category ?? '') ||
          draftReviewStatus !== (activeKeyMemory.review_status ?? 'pending')
        )
      );
      const creatingDirty = Boolean(createKey.trim() || createValue.trim() || createCategory.trim() || createReviewStatus !== 'pending');
      return editingDirty || creatingDirty;
    },
    'This key memory workspace has unsaved edits. Closing or reloading the page may discard the draft kept in this tab.'
  );

  useSessionDraftPersistence(() => {
    if (activeDraftKey) {
      writeSessionDraft(activeDraftKey, keyMemoriesDraftVersion, currentDraft);
    }
  });

  function emptyDraft(): KeyMemoriesDraft {
    return {
      filter: '',
      dateFrom: '',
      dateTo: '',
      selectedTiers: [],
      editingId: '',
      draftValue: '',
      draftCategory: '',
      draftReviewStatus: 'pending',
      createKey: '',
      createValue: '',
      createCategory: '',
      createReviewStatus: 'pending'
    };
  }

  function applyDraft(draft: KeyMemoriesDraft | null, nextKeyMemories: KeyMemoryRecord[] = keyMemories): void {
    filter = draft?.filter ?? '';
    dateFrom = draft?.dateFrom ?? '';
    dateTo = draft?.dateTo ?? '';
    const allowedTiers = new Set(timeTierOptions.map((item) => item.value));
    selectedTiers = (draft?.selectedTiers ?? []).filter((item) => allowedTiers.has(item));

    const requestedId = draft?.editingId ?? '';
    const activeKeyMemory = requestedId ? nextKeyMemories.find((item) => item.id === requestedId) : null;
    editingId = activeKeyMemory?.id ?? '';
    draftValue = activeKeyMemory ? draft?.draftValue ?? activeKeyMemory.value : '';
    draftCategory = activeKeyMemory ? draft?.draftCategory ?? activeKeyMemory.category ?? '' : '';
    draftReviewStatus = activeKeyMemory ? draft?.draftReviewStatus ?? activeKeyMemory.review_status ?? 'pending' : 'pending';

    createKey = draft?.createKey ?? '';
    createValue = draft?.createValue ?? '';
    createCategory = draft?.createCategory ?? '';
    createReviewStatus = draft?.createReviewStatus ?? 'pending';
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
      const nextKeyMemories = await fetchAdminKeyMemories($selectedTarget, {
        userId: activeUserId,
        limit: 200,
        query: filter.trim(),
        dateFrom: startOfDayISO(dateFrom),
        dateTo: endOfDayISO(dateTo),
        timeTiers: selectedTiers
      });
      keyMemories = nextKeyMemories;
      hydratingDraft = true;
      applyDraft(readSessionDraft<KeyMemoriesDraft>(keyMemoriesDraftKey, keyMemoriesDraftVersion), nextKeyMemories);
      hydratingDraft = false;
      draftReady = true;
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
  <PageHeader
    eyebrow="Inspect / Memory"
    title="Key memories"
    description="Whitelisted slots backing memory tools and admin workflows: preferences, recent markers, operator-curated notes."
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  />

  <MemoryWorkspaceNav current="/memory/keys" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect key memories.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading key memories…</div>
  {:else}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}

    <section class="card page-stack">
      <div class="page-header">
        <div class="page-header-meta">
          <h2 class="section-title">Create key memory</h2>
          <p class="panel-meta">New writes are audited on the target.</p>
        </div>
      </div>
      <div class="form-grid two-up">
        <TextInputField label="Key" bind:value={createKey} placeholder="preferred_name or recent_conversation" />
        <TextInputField label="Category" bind:value={createCategory} placeholder="profile, workflow, system" />
        <TextareaField label="Value" bind:value={createValue} rows={4} fieldClass="field-span-2" />
        <SelectField label="Review status" items={reviewStatusItems} bind:value={createReviewStatus} />
      </div>
      <div class="inline-group" style="justify-content: flex-end;">
        <button class="btn-primary" type="button" disabled={creating || !createKey.trim() || !createValue.trim()} on:click={createItem}>
          {creating ? 'Creating…' : 'Create key memory'}
        </button>
      </div>
    </section>

    <section class="card page-stack">
      <div class="page-header">
        <div class="page-header-meta">
          <h2 class="section-title">Filters</h2>
          <p class="panel-meta">{visibleKeyMemories.length} key memor{visibleKeyMemories.length === 1 ? 'y' : 'ies'} returned</p>
        </div>
        <button class="btn-secondary btn-sm" type="button" on:click={loadData}>Apply filters</button>
      </div>
      <div class="filters two-up">
        <TextInputField
          label="Filter"
          bind:value={filter}
          placeholder="key, category, value, review status…"
          fieldClass="field-span-2"
        />
        <TextInputField label="Date from" bind:value={dateFrom} type="date" />
        <TextInputField label="Date to" bind:value={dateTo} type="date" />
      </div>
      <div class="inline-group">
        <span class="field-label">Time tier</span>
        <div class="segmented-control" style="flex-wrap: wrap;">
          {#each timeTierOptions as option}
            <button
              class="segmented-control-item"
              type="button"
              data-state={selectedTiers.includes(option.value) ? 'on' : 'off'}
              on:click={() => toggleTier(option.value)}
            >{option.label}</button>
          {/each}
        </div>
      </div>

      {#if visibleKeyMemories.length === 0}
        <div class="empty-state">No key memories match the current filter.</div>
      {:else}
        <div class="stack-list">
          {#each visibleKeyMemories as keyMemory}
            <article class="keymem-card">
              <header class="keymem-head">
                <div class="keymem-head-main">
                  <strong>{keyMemory.key}</strong>
                  <span class="helper-text mono">{keyMemory.id}</span>
                </div>
                <div class="inline-group">
                  {#if keyMemory.time_tier}
                    <StatusBadge tone="muted" label={keyMemory.time_tier} />
                  {/if}
                  <StatusBadge tone="muted" label={keyMemory.category ?? 'uncategorized'} />
                  <StatusBadge
                    tone={keyMemory.review_status === 'approved' ? 'success' : keyMemory.review_status === 'rejected' ? 'danger' : 'warning'}
                    label={keyMemory.review_status ?? 'pending'}
                  />
                  {#if keyMemory.effective_score !== undefined}
                    <StatusBadge tone="brand" label={`effective ${keyMemory.effective_score.toFixed(3)}`} />
                  {/if}
                </div>
              </header>

              <div class="keymem-actions">
                <a class="btn-ghost btn-sm" href={memoryDetailHref('keys', keyMemory.id, activeUserId)}>Open</a>
                {#if editingId === keyMemory.id}
                  <button class="btn-ghost btn-sm" type="button" on:click={cancelEdit}>Cancel</button>
                  <button class="btn-primary btn-sm" type="button" disabled={saving} on:click={() => saveEdit(keyMemory)}>
                    {saving ? 'Saving…' : 'Save'}
                  </button>
                {:else}
                  <button class="btn-secondary btn-sm" type="button" on:click={() => startEdit(keyMemory)}>Edit</button>
                {/if}
                <button class="btn-danger btn-sm" type="button" disabled={deletingId === keyMemory.id} on:click={() => removeItem(keyMemory)}>
                  {deletingId === keyMemory.id ? 'Deleting…' : 'Delete'}
                </button>
              </div>

              {#if editingId === keyMemory.id}
                <div class="form-grid two-up">
                  <TextareaField label="Value" bind:value={draftValue} rows={4} fieldClass="field-span-2" />
                  <TextInputField label="Category" bind:value={draftCategory} />
                  <SelectField label="Review status" items={reviewStatusItems} bind:value={draftReviewStatus} />
                </div>
              {:else}
                <div class="helper-text mono">
                  activity {dateTimeLabel(keyMemory.activity_at)} · importance {(keyMemory.importance ?? 0).toFixed(3)} · decay {(keyMemory.decay_factor ?? 0).toFixed(3)}
                </div>
                <JsonView value={keyMemory} />
              {/if}
            </article>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>

<style>
  .keymem-card {
    display: grid;
    gap: 8px;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
  }

  .keymem-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  .keymem-head-main {
    display: grid;
    gap: 2px;
  }

  .keymem-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
</style>
