<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import { onMount } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { Plus, X } from '@lucide/svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import {
    createAdminKeyMemory,
    deleteAdminKeyMemory,
    fetchAdminKeyMemories,
    updateAdminKeyMemory
  } from '#lib/api/client.ts';
  import ConfirmActionDialog from '#lib/components/ConfirmActionDialog.svelte';
  import JsonView from '#lib/components/JsonView.svelte';
  import MemoryWorkspaceNav from '#lib/components/MemoryWorkspaceNav.svelte';
  import FilterToolbar from '#lib/components/FilterToolbar.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import { memoryDetailHref } from '#lib/memory/routes.ts';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
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
  import type { KeyMemoryRecord } from '#lib/types.ts';

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
  let createOpen = false;
  let createError = '';
  let deletingId = '';
  let deleteCandidate: KeyMemoryRecord | null = null;
  let error = '';
  let success = '';
  let filter = '';
  let dateFrom = '';
  let dateTo = '';
  let selectedTiers: string[] = [];
  let lastTargetKey = '';
  let mounted = false;
  let loadRevision = 0;
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
  $: if (mounted && routeKey !== lastTargetKey && !$targetState.session.checking) {
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

  onMount(() => {
    mounted = true;
    return () => { loadRevision++; };
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
    const revision = ++loadRevision;
    const scope = routeKey;
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
      if (revision !== loadRevision || scope !== routeKey) return;
      keyMemories = nextKeyMemories;
      hydratingDraft = true;
      applyDraft(readSessionDraft<KeyMemoriesDraft>(keyMemoriesDraftKey, keyMemoriesDraftVersion), nextKeyMemories);
      hydratingDraft = false;
      draftReady = true;
    } catch (cause) {
      if (revision === loadRevision && scope === routeKey) error = cause instanceof Error ? cause.message : 'Failed to load key memories';
    } finally {
      if (revision === loadRevision && scope === routeKey) loading = false;
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
    if (creating || !$selectedTarget || !createKey.trim() || !createValue.trim()) {
      return;
    }
    creating = true;
    createError = '';
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
      createOpen = false;
      success = 'Key memory created and audited on the target.';
    } catch (cause) {
      createError = cause instanceof Error ? cause.message : 'Failed to create key memory';
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
</script>

<Dialog.Root bind:open={createOpen}>
<section class="page-stack">
  <PageHeader
    eyebrow="Inspect / Memory"
    title="Key memories"
    description="Whitelisted slots backing memory tools and admin workflows: preferences, recent markers, operator-curated notes."
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  >
    <div slot="actions">
      <Dialog.Trigger class="btn-primary" disabled={!$targetState.session.authenticated} onclick={() => (createError = '')}><Plus size={15} /> New key memory</Dialog.Trigger>
    </div>
  </PageHeader>

  <MemoryWorkspaceNav current="/memory/keys" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect key memories.</div>
  {:else}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}

    <section class="card page-stack">
      <form on:submit|preventDefault={loadData}>
      <FilterToolbar activeCount={Number(Boolean(dateFrom)) + Number(Boolean(dateTo)) + selectedTiers.length} summary={`${visibleKeyMemories.length} key memories`}>
        <TextInputField label="Filter" bind:value={filter} placeholder="Search keys and values…" />
        {#snippet actions()}
          <Button class="btn-secondary" type="submit" disabled={loading}>{loading ? 'Loading…' : 'Apply filters'}</Button>
        {/snippet}
        {#snippet filters()}
          <div class="filters two-up">
            <TextInputField label="Date from" bind:value={dateFrom} type="date" />
            <TextInputField label="Date to" bind:value={dateTo} type="date" />
          </div>
          <div class="field">
            <span class="field-label">Time tier</span>
            <div class="segmented-control" style="flex-wrap: wrap;">
              {#each timeTierOptions as option}
                <Button class="segmented-control-item" type="button" data-state={selectedTiers.includes(option.value) ? 'on' : 'off'} aria-pressed={selectedTiers.includes(option.value)} onclick={() => toggleTier(option.value)}>{option.label}</Button>
              {/each}
            </div>
          </div>
        {/snippet}
      </FilterToolbar>
      </form>

      {#if error}
        <div class="error-state" role="alert">{error}</div>
      {:else if loading}
        <div class="notice-card">Loading key memories…</div>
      {:else if visibleKeyMemories.length === 0}
        <div class="empty-state">No key memories match the current filter.</div>
      {:else}
        <div class="stack-list data-list">
          {#each visibleKeyMemories as keyMemory}
            <article class="keymem-card data-list-row">
              <header class="keymem-head">
                <div class="keymem-head-main">
                  <strong>{keyMemory.key}</strong>
                </div>
                <div class="inline-group">
                  {#if keyMemory.category}<span class="helper-text">{keyMemory.category}</span>{/if}
                  {#if keyMemory.review_status !== 'approved'}
                    <StatusBadge tone={keyMemory.review_status === 'rejected' ? 'danger' : 'warning'} label={keyMemory.review_status ?? 'pending'} />
                  {/if}
                </div>
              </header>

              {#if editingId !== keyMemory.id}
                <div class="keymem-value">{keyMemory.value}</div>
              {/if}
              <div class="keymem-actions">
                <a class="btn-ghost btn-sm" href={memoryDetailHref('keys', keyMemory.id, activeUserId)}>Open</a>
                {#if editingId === keyMemory.id}
                  <Button class="btn-ghost btn-sm" type="button" onclick={cancelEdit}>Cancel</Button>
                  <Button class="btn-primary btn-sm" type="button" disabled={saving} onclick={() => saveEdit(keyMemory)}>
                    {saving ? 'Saving…' : 'Save'}
                  </Button>
                {:else}
                  <Button class="btn-secondary btn-sm" type="button" onclick={() => startEdit(keyMemory)}>Edit</Button>
                {/if}
                <Button class="btn-danger is-quiet btn-sm" type="button" disabled={deletingId === keyMemory.id} onclick={() => (deleteCandidate = keyMemory)}>
                  {deletingId === keyMemory.id ? 'Deleting…' : 'Delete'}
                </Button>
              </div>

              {#if editingId === keyMemory.id}
                <div class="form-grid two-up">
                  <TextareaField label="Value" bind:value={draftValue} rows={4} fieldClass="field-span-2" />
                  <TextInputField label="Category" bind:value={draftCategory} />
                  <SelectField label="Review status" items={reviewStatusItems} bind:value={draftReviewStatus} />
                </div>
              {:else}
                <CollapsibleSection title="Record details">
                  <JsonView value={keyMemory} />
                </CollapsibleSection>
              {/if}
            </article>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>

  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="dialog-card" escapeKeydownBehavior={creating ? 'ignore' : 'close'} interactOutsideBehavior={creating ? 'ignore' : 'close'}>
      <div class="page-header keymem-dialog-header">
        <div class="page-header-meta">
          <Dialog.Title class="section-title">Create key memory</Dialog.Title>
          <Dialog.Description class="helper-text">Add a preference or note. New writes are audited.</Dialog.Description>
        </div>
        <Dialog.Close class="btn-ghost btn-icon" aria-label="Close" disabled={creating}><X size={16} aria-hidden="true" /></Dialog.Close>
      </div>
      <form class="page-stack" on:submit|preventDefault={createItem}>
        {#if createError}<p class="error-state" role="alert">{createError}</p>{/if}
      <div class="form-grid two-up">
        <TextInputField label="Key" required bind:value={createKey} placeholder="preferred_name or recent_conversation" />
        <TextInputField label="Category" bind:value={createCategory} placeholder="profile, workflow, system" />
        <TextareaField label="Value" required bind:value={createValue} rows={4} fieldClass="field-span-2" />
        <SelectField label="Review status" items={reviewStatusItems} bind:value={createReviewStatus} />
      </div>
      <div class="inline-group" style="justify-content: flex-end;">
        <Dialog.Close class="btn-secondary" disabled={creating}>Cancel</Dialog.Close>
        <Button class="btn-primary" type="submit" disabled={creating || !createKey.trim() || !createValue.trim()}>
          {creating ? 'Creating…' : 'Create key memory'}
        </Button>
      </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<ConfirmActionDialog
  open={Boolean(deleteCandidate)}
  title="Delete key memory?"
  description="This permanently removes the key memory from active recall and memory tools."
  subject={deleteCandidate ? `${deleteCandidate.key} · ${deleteCandidate.id}` : ''}
  confirmLabel="Delete key memory"
  busy={Boolean(deletingId)}
  on:cancel={() => (deleteCandidate = null)}
  on:confirm={async () => {
    if (!deleteCandidate) return;
    const item = deleteCandidate;
    await removeItem(item);
    deleteCandidate = null;
  }}
/>

<style>
  .keymem-dialog-header { align-items: flex-start; flex-wrap: nowrap; }
  .keymem-value { white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.65; }

  .keymem-card {
    display: grid;
    gap: 8px;
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
