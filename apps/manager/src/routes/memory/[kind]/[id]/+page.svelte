<svelte:options runes={false} />

<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { onMount } from 'svelte';

  import {
    deleteAdminKeyMemory,
    fetchConversation,
    fetchAdminKeyMemory,
    fetchAdminMemoryDocument,
    fetchAudit,
    updateAdminKeyMemory,
    updateAdminMemoryDocument
  } from '$lib/api/client';
  import ChatMessageCard from '$lib/components/ChatMessageCard.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import MemoryKeywordSignals from '$lib/components/MemoryKeywordSignals.svelte';
  import MemoryRelationGraph from '$lib/components/MemoryRelationGraph.svelte';
  import SelectField from '$lib/components/SelectField.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import TextareaField from '$lib/components/TextareaField.svelte';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '$lib/memory/keyword-signals';
  import {
    documentKindFromRoute,
    memoryCollectionHref,
    memoryDetailHref,
    memoryRouteKindFromDocumentType,
    normalizeMemoryRouteKind,
    type MemoryRouteKind
  } from '$lib/memory/routes';
  import {
    buildSessionDraftKey,
    clearSessionDraft,
    draftsEqual,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '$lib/stores/session-drafts';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type {
    AuditEventRecord,
    ConversationRecord,
    KeyMemoryRecord,
    MemoryDocument,
    MemoryEvidence,
    MessagePart,
    MessagePartArtifact
  } from '$lib/types';

  type MemoryDetailDraft = {
    draftContent: string;
    draftValue: string;
    draftCategory: string;
    draftReviewStatus: string;
  };

  const memoryDetailDraftVersion = 1;

  let loading = true;
  let saving = false;
  let deleting = false;
  let error = '';
  let success = '';
  let audits: AuditEventRecord[] = [];
  let documentItem: MemoryDocument | null = null;
  let keyMemoryItem: KeyMemoryRecord | null = null;
  let sourceConversation: ConversationRecord | null = null;
  let draftContent = '';
  let draftValue = '';
  let draftCategory = '';
  let draftReviewStatus = 'pending';
  let lastRouteKey = '';
  let activeUserId: string | undefined;
  let routeKind: MemoryRouteKind | null = null;
  let routeKey = '';
  let itemTitle = 'Memory Item';
  let itemDescription = 'Inspect stored memory detail.';
  let currentCollectionHref = '/memory';
  let currentItemHref = '/memory';
  let sourcePreview: ConversationRecord['messages'] = [];
  let sourcePartsByID = new Map<string, MessagePart>();
  let sourceArtifactsByID = new Map<string, MessagePartArtifact>();
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let savedDraft = emptyDraft();
  let currentDraft = emptyDraft();
  const reviewStatusItems = [
    { value: 'pending', label: 'pending' },
    { value: 'approved', label: 'approved' },
    { value: 'rejected', label: 'rejected' }
  ];

  $: activeUserId = $page.url.searchParams.get('user_id') ?? undefined;
  $: routeKind = normalizeMemoryRouteKind($page.params.kind ?? '');
  $: memoryDetailDraftKey = buildSessionDraftKey(
    'memory.detail',
    $selectedTarget?.id,
    `${$page.params.kind ?? 'unknown'}:${$page.params.id ?? 'unknown'}:${activeUserId ?? 'admin'}`
  );
  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${$page.params.kind}:${$page.params.id}:${activeUserId ?? 'admin'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;

  onMount(async () => {
    await loadDetail();
  });

  $: if (routeKey !== lastRouteKey && !$targetState.session.checking) {
    lastRouteKey = routeKey;
    void loadDetail();
  }

  $: itemTitle = titleForKind(routeKind);
  $: itemDescription = descriptionForKind(routeKind);
  $: currentCollectionHref = routeKind ? memoryCollectionHref(routeKind, activeUserId) : '/memory';
  $: currentItemHref = routeKind ? memoryDetailHref(routeKind, $page.params.id ?? '', activeUserId) : '/memory';
  $: sourcePreview = sourceConversation?.messages?.slice(-4) ?? [];
  $: sourcePartsByID = new Map((sourceConversation?.parts ?? []).map((part) => [part.part_id, part]));
  $: sourceArtifactsByID = new Map((sourceConversation?.artifacts ?? []).map((artifact) => [artifact.artifact_id, artifact]));
  $: currentDraft = captureDraft();
  $: if (draftReady && activeDraftKey && !hydratingDraft) {
    if (draftsEqual(currentDraft, savedDraft)) {
      clearSessionDraft(activeDraftKey);
    } else {
      writeSessionDraft(activeDraftKey, memoryDetailDraftVersion, currentDraft);
    }
  }

  useSessionDraftUnloadWarning(
    () => draftReady && !draftsEqual(currentDraft, savedDraft),
    'This memory detail page has unsaved edits. Closing or reloading the page may discard the draft kept in this tab.'
  );

  useSessionDraftPersistence(() => {
    if (!activeDraftKey) {
      return;
    }
    const snapshot = captureDraft();
    if (draftsEqual(snapshot, savedDraft)) {
      clearSessionDraft(activeDraftKey);
      return;
    }
    writeSessionDraft(activeDraftKey, memoryDetailDraftVersion, snapshot);
  });

  function emptyDraft(): MemoryDetailDraft {
    return {
      draftContent: '',
      draftValue: '',
      draftCategory: '',
      draftReviewStatus: 'pending'
    };
  }

  function captureDraft(): MemoryDetailDraft {
    return {
      draftContent,
      draftValue,
      draftCategory,
      draftReviewStatus
    };
  }

  function applyDraft(draft: MemoryDetailDraft | null): void {
    if (!draft) {
      return;
    }
    if (routeKind === 'keys') {
      draftValue = draft.draftValue ?? draftValue;
      draftCategory = draft.draftCategory ?? draftCategory;
      draftReviewStatus = draft.draftReviewStatus ?? draftReviewStatus;
      return;
    }
    draftContent = draft.draftContent ?? draftContent;
  }

  async function loadDetail(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !routeKind || !$page.params.id) {
      loading = false;
      draftReady = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    documentItem = null;
    keyMemoryItem = null;
    sourceConversation = null;

    try {
      if (routeKind === 'keys') {
        const [item, auditItems] = await Promise.all([
          fetchAdminKeyMemory($selectedTarget, $page.params.id, activeUserId),
          fetchAudit($selectedTarget, {
            limit: 50,
            targetType: 'key_memory',
            targetId: $page.params.id
          })
        ]);
        keyMemoryItem = item;
        draftValue = item.value;
        draftCategory = item.category ?? '';
        draftReviewStatus = item.review_status ?? 'pending';
        audits = auditItems;
      } else {
        const documentKind = documentKindFromRoute(routeKind);
        if (!documentKind) {
          throw new Error(`Unsupported memory route kind: ${routeKind}`);
        }
        const [item, auditItems] = await Promise.all([
          fetchAdminMemoryDocument($selectedTarget, documentKind, $page.params.id, activeUserId),
          fetchAudit($selectedTarget, {
            limit: 50,
            targetType: documentKind,
            targetId: $page.params.id
          })
        ]);
        documentItem = item;
        draftContent = item.content;
        audits = auditItems;
        if (item.conversation_id) {
          sourceConversation = await fetchConversation($selectedTarget, item.conversation_id, item.user_id).catch(() => null);
        }
      }
      savedDraft = captureDraft();
      activeDraftKey = memoryDetailDraftKey;
      hydratingDraft = true;
      applyDraft(readSessionDraft<MemoryDetailDraft>(memoryDetailDraftKey, memoryDetailDraftVersion));
      hydratingDraft = false;
      draftReady = true;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load memory detail';
    } finally {
      loading = false;
    }
  }

  async function save(): Promise<void> {
    if (!$selectedTarget || !routeKind) {
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      if (routeKind === 'keys') {
        if (!keyMemoryItem) {
          return;
        }
        keyMemoryItem = await updateAdminKeyMemory($selectedTarget, keyMemoryItem.id, {
          userId: activeUserId,
          value: draftValue,
          category: draftCategory,
          reviewStatus: draftReviewStatus
        });
      } else {
        const documentKind = documentKindFromRoute(routeKind);
        if (!documentKind || !documentItem) {
          return;
        }
        documentItem = await updateAdminMemoryDocument($selectedTarget, documentKind, documentItem.id, draftContent, activeUserId);
      }
      await loadDetail();
      success = routeKind === 'keys' ? 'Key memory updated and reloaded from the target.' : `${itemTitle} updated and reloaded from the target.`;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save memory detail';
    } finally {
      saving = false;
    }
  }

  async function deleteKeyMemory(): Promise<void> {
    if (!$selectedTarget || !keyMemoryItem) {
      return;
    }
    deleting = true;
    error = '';
    success = '';
    try {
      await deleteAdminKeyMemory($selectedTarget, keyMemoryItem.id, activeUserId);
      await goto(currentCollectionHref);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to delete key memory';
    } finally {
      deleting = false;
    }
  }

  function titleForKind(kind: MemoryRouteKind | null): string {
    switch (kind) {
      case 'summaries':
        return 'Summary';
      case 'impressions':
        return 'Impression';
      case 'facts':
        return 'Fact';
      case 'keys':
        return 'Key Memory';
      default:
        return 'Memory Item';
    }
  }

  function descriptionForKind(kind: MemoryRouteKind | null): string {
    switch (kind) {
      case 'summaries':
        return 'Inspect the source conversation, edit the rollup text, and verify downstream audit history.';
      case 'impressions':
        return 'Review soft-signal analysis output, linked relations, and manual correction history.';
      case 'facts':
        return 'Validate factual statements, graph links, and operator changes before they affect recall.';
      case 'keys':
        return 'Manage a stable operator-curated key slot and inspect every audited mutation.';
      default:
        return 'Inspect stored memory detail.';
    }
  }

  function reviewTone(value?: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (value === 'approved') {
      return 'success';
    }
    if (value === 'rejected') {
      return 'danger';
    }
    if (value === 'pending') {
      return 'warning';
    }
    return 'muted';
  }

  function auditTone(action: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (action.includes('delete') || action.includes('revoke')) {
      return 'danger';
    }
    if (action.includes('create')) {
      return 'success';
    }
    if (action.includes('update') || action.includes('requeue')) {
      return 'brand';
    }
    if (action.includes('tool')) {
      return 'warning';
    }
    return 'muted';
  }

  function dateTimeLabel(value?: string): string {
    if (!value) {
      return 'n/a';
    }
    return new Date(value).toLocaleString();
  }

  function temporalEntries(metadata?: Record<string, unknown>): Array<[string, string]> {
    const temporal = metadata?.temporal;
    if (!temporal || typeof temporal !== 'object' || Array.isArray(temporal)) {
      return [];
    }
    return Object.entries(temporal as Record<string, unknown>)
      .filter(([, value]) => typeof value === 'string' && value.length > 0)
      .map(([key, value]) => [key, String(value)]);
  }

  function formatMilliseconds(value?: number): string {
    if (value === undefined || value < 0) {
      return '';
    }
    const totalSeconds = Math.floor(value / 1000);
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    const millis = value % 1000;
    return `${minutes}:${String(seconds).padStart(2, '0')}.${String(millis).padStart(3, '0')}`;
  }

  function formatLocator(evidence: MemoryEvidence, part?: MessagePart, artifact?: MessagePartArtifact): string {
    const labels: string[] = [];
    const sequenceNum = part?.sequence_num ?? evidence.sequence_num;
    labels.push(`sequence ${sequenceNum}`);
    if ((artifact?.page_index ?? part?.page_index ?? evidence.page_index) !== undefined) {
      labels.push(`page ${artifact?.page_index ?? part?.page_index ?? evidence.page_index}`);
    }
    const start = artifact?.start_ms ?? evidence.start_ms;
    const end = artifact?.end_ms ?? evidence.end_ms;
    if (start !== undefined || end !== undefined) {
      const startLabel = formatMilliseconds(start ?? 0) || '0:00.000';
      const endLabel = formatMilliseconds(end ?? 0);
      labels.push(endLabel ? `${startLabel} -> ${endLabel}` : startLabel);
    }
    return labels.join(' · ');
  }

  function evidencePreview(evidence: MemoryEvidence, part?: MessagePart, artifact?: MessagePartArtifact): string {
    return evidence.excerpt_text ?? artifact?.text_content ?? part?.text_content ?? 'No excerpt captured.';
  }

  function evidenceTone(kind?: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (kind === 'artifact') {
      return 'brand';
    }
    if (kind === 'source_part') {
      return 'success';
    }
    if (kind === 'segment') {
      return 'warning';
    }
    return 'muted';
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Memory / {itemTitle}</span>
    <h1 class="page-title">{$page.params.id}</h1>
    <p class="page-description">{itemDescription}</p>
    {#if activeUserId}
      <div class="helper-text">Scoped to user `{activeUserId}`.</div>
    {/if}
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect memory detail.</div>
  {:else if !routeKind}
    <div class="error-state">Unsupported memory kind: {$page.params.kind}</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading memory detail...</div>
  {:else}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}

    <div class="inline-group" style="justify-content: space-between; flex-wrap: wrap;">
      <div class="inline-group" style="flex-wrap: wrap;">
        <a class="button-ghost" href={currentCollectionHref}>Back to collection</a>
        <a class="button-ghost" href="/memory">Memory workspace</a>
        {#if documentItem?.conversation_id}
          <a class="button-ghost" href={`/conversations/${documentItem.conversation_id}?user_id=${encodeURIComponent(documentItem.user_id)}`}>Open source conversation</a>
        {/if}
        {#if documentItem || keyMemoryItem}
          <a class="button-ghost" href={`/users/${encodeURIComponent((documentItem?.user_id ?? keyMemoryItem?.user_id) ?? 'admin')}`}>Open user</a>
        {/if}
      </div>
      <div class="inline-group" style="flex-wrap: wrap;">
        {#if routeKind === 'keys'}
          <button class="button-danger" type="button" disabled={deleting} on:click={deleteKeyMemory}>{deleting ? 'Deleting...' : 'Delete key memory'}</button>
        {/if}
        <button class="button" type="button" disabled={saving} on:click={save}>{saving ? 'Saving...' : 'Save changes'}</button>
      </div>
    </div>

    <div class="grid-2" style="align-items: start;">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Core Detail</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Editable content and runtime metadata</h2>
          </div>
          <div class="runtime-field-status">
            <StatusBadge tone="brand" label={itemTitle} />
            {#if documentItem}
              <StatusBadge tone="muted" label={documentItem.conversation_id ? 'conversation-linked' : 'manual'} />
            {/if}
            {#if keyMemoryItem}
              <StatusBadge tone={reviewTone(keyMemoryItem.review_status)} label={keyMemoryItem.review_status ?? 'pending'} />
            {/if}
          </div>
        </div>

        {#if documentItem}
          <div class="toolbar">
            <div class="grid-2">
              <div class="debug-block">
                <div class="sidebar-label">User</div>
                <div>{documentItem.user_id}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Node</div>
                <div>{documentItem.node_id}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Importance</div>
                <div>{documentItem.importance ?? 0}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Decay Factor</div>
                <div>{documentItem.decay_factor ?? 0}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Created</div>
                <div>{documentItem.created_at ? new Date(documentItem.created_at).toLocaleString() : 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Updated</div>
                <div>{documentItem.updated_at ? new Date(documentItem.updated_at).toLocaleString() : 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Activity Anchor</div>
                <div>{dateTimeLabel(documentItem.activity_at)}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Time Tier</div>
                <div>{documentItem.time_tier ?? 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Effective Score</div>
                <div>{documentItem.effective_score?.toFixed?.(3) ?? 'n/a'}</div>
              </div>
            </div>

            <TextareaField label="Content" bind:value={draftContent} rows={10} />

            <div class="debug-block">
              <div class="page-header">
                <div>
                  <div class="sidebar-label">Metadata</div>
                  <h3 style="margin: 0;">Stored metadata</h3>
                </div>
                <a class="button-ghost" href={currentItemHref}>Permalink</a>
              </div>
              <MemoryKeywordSignals
                matchedKeywords={matchedKeywordsFromMetadata(documentItem.metadata)}
                sourceKeywords={sourceKeywordsFromMetadata(documentItem.metadata)}
              />
              <JsonView value={documentItem.metadata ?? {}} emptyLabel="No metadata recorded." />
              {#if temporalEntries(documentItem.metadata).length > 0}
                <div class="list-table" style="margin-top: 12px;">
                  {#each temporalEntries(documentItem.metadata) as [key, value]}
                    <div class="table-row-link">
                      <span>{key}</span>
                      <span class="subtle-text">{value}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {:else if keyMemoryItem}
          <div class="toolbar">
            <div class="grid-2">
              <div class="debug-block">
                <div class="sidebar-label">User</div>
                <div>{keyMemoryItem.user_id}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Key</div>
                <div>{keyMemoryItem.key}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Source</div>
                <div>{keyMemoryItem.source ?? 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Updated</div>
                <div>{keyMemoryItem.updated_at ? new Date(keyMemoryItem.updated_at).toLocaleString() : 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Activity Anchor</div>
                <div>{dateTimeLabel(keyMemoryItem.activity_at)}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Time Tier</div>
                <div>{keyMemoryItem.time_tier ?? 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Effective Score</div>
                <div>{keyMemoryItem.effective_score?.toFixed?.(3) ?? 'n/a'}</div>
              </div>
              <div class="debug-block">
                <div class="sidebar-label">Importance / Decay</div>
                <div>{(keyMemoryItem.importance ?? 0).toFixed(3)} / {(keyMemoryItem.decay_factor ?? 0).toFixed(3)}</div>
              </div>
            </div>

            <TextareaField label="Value" bind:value={draftValue} rows={8} />

            <div class="form-grid two-up">
              <TextInputField label="Category" bind:value={draftCategory} />
              <SelectField label="Review status" items={reviewStatusItems} bind:value={draftReviewStatus} />
            </div>

            <div class="debug-block">
              <div class="page-header">
                <div>
                  <div class="sidebar-label">Metadata</div>
                  <h3 style="margin: 0;">Stored metadata</h3>
                </div>
                <a class="button-ghost" href={currentItemHref}>Permalink</a>
              </div>
              <JsonView value={keyMemoryItem.metadata ?? {}} emptyLabel="No metadata recorded." />
              {#if temporalEntries(keyMemoryItem.metadata).length > 0}
                <div class="list-table" style="margin-top: 12px;">
                  {#each temporalEntries(keyMemoryItem.metadata) as [key, value]}
                    <div class="table-row-link">
                      <span>{key}</span>
                      <span class="subtle-text">{value}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {/if}
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Context</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Links, graph neighbors, and provenance</h2>
          </div>
        </div>

        {#if documentItem}
          <div class="toolbar">
            <div class="debug-block">
              <div class="sidebar-label">Source conversation</div>
              {#if documentItem.conversation_id}
                <div class="toolbar">
                  <a href={`/conversations/${documentItem.conversation_id}?user_id=${encodeURIComponent(documentItem.user_id)}`}>{documentItem.conversation_id}</a>
                  {#if sourceConversation}
                    <div class="helper-text">
                      {sourceConversation.messages.length} messages · updated {new Date(sourceConversation.updated_at).toLocaleString()}
                    </div>
                  {/if}
                </div>
              {:else}
                <div>None linked.</div>
              {/if}
            </div>

            <div class="debug-block">
              <div class="sidebar-label">Related nodes</div>
              {#if (documentItem.related?.length ?? 0) === 0}
                <div class="helper-text">No bounded relation expansion was returned for this document.</div>
              {:else}
                <div style="margin-bottom: 14px;">
                  <MemoryRelationGraph centerLabel={documentItem.id} centerType={documentItem.type} items={documentItem.related ?? []} />
                </div>
                <div class="list-table">
                  {#each documentItem.related ?? [] as relation}
                    <div class="table-row-link">
                      <div>
                        <div style="font-weight: 600;">{relation.id}</div>
                        <div class="subtle-text">{relation.type}</div>
                      </div>
                      <div class="inline-group" style="flex-wrap: wrap; justify-content: flex-end;">
                        <StatusBadge tone="muted" label={`weight ${relation.weight.toFixed(2)}`} />
                        {#if memoryRouteKindFromDocumentType(relation.type)}
                          <a
                            class="button-ghost"
                            href={memoryDetailHref(memoryRouteKindFromDocumentType(relation.type) ?? 'summaries', relation.id, documentItem.user_id)}
                          >Open</a>
                        {/if}
                      </div>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>

            <div class="debug-block">
              <div class="sidebar-label">Temporal indexing</div>
              {#if temporalEntries(documentItem.metadata).length === 0}
                <div class="helper-text">No temporal graph metadata is attached to this document.</div>
              {:else}
                <div class="list-table">
                  {#each temporalEntries(documentItem.metadata) as [key, value]}
                    <div class="table-row-link">
                      <span>{key}</span>
                      <span class="subtle-text">{value}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>

            <div class="debug-block">
              <div class="sidebar-label">Conversation excerpt</div>
              {#if !documentItem.conversation_id}
                <div class="helper-text">This memory item is not linked to a conversation.</div>
              {:else if !sourceConversation}
                <div class="helper-text">The linked conversation is not currently available for preview.</div>
              {:else if sourcePreview.length === 0}
                <div class="helper-text">No message preview is available for this conversation.</div>
              {:else}
                <div class="toolbar">
                  {#each sourcePreview as message, index}
                    <ChatMessageCard message={{ ...message, id: `${sourceConversation.conversation_id}-${index}` }} />
                  {/each}
                </div>
              {/if}
            </div>

            <div class="debug-block">
              <div class="page-header">
                <div>
                  <div class="sidebar-label">Evidence</div>
                  <h3 style="margin: 0;">Source parts and derived artifacts</h3>
                </div>
                <StatusBadge tone="muted" label={`${documentItem.evidences?.length ?? 0} item${(documentItem.evidences?.length ?? 0) === 1 ? '' : 's'}`} />
              </div>
              {#if (documentItem.evidences?.length ?? 0) === 0}
                <div class="helper-text">No source evidence is attached to this memory document yet.</div>
              {:else}
                <div class="toolbar">
                  {#each documentItem.evidences ?? [] as evidence}
                    {@const part = evidence.part_id ? sourcePartsByID.get(evidence.part_id) : undefined}
                    {@const artifact = evidence.artifact_id ? sourceArtifactsByID.get(evidence.artifact_id) : undefined}
                    <div class="debug-block" style="background: var(--bg-panel-muted);">
                      <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px; align-items: flex-start;">
                        <div>
                          <strong>{evidence.evidence_kind}</strong>
                          <div class="subtle-text">{formatLocator(evidence, part, artifact)}</div>
                        </div>
                        <div class="runtime-field-status">
                          <StatusBadge tone={evidenceTone(evidence.evidence_kind)} label={artifact?.artifact_type ?? part?.part_type ?? 'evidence'} />
                          {#if evidence.weight !== undefined}
                            <StatusBadge tone="muted" label={`weight ${evidence.weight.toFixed(2)}`} />
                          {/if}
                        </div>
                      </div>
                      <div class="message-body">{evidencePreview(evidence, part, artifact)}</div>
                      <div class="inline-group" style="margin-top: 10px; flex-wrap: wrap;">
                        {#if part}
                          <StatusBadge tone="muted" label={`part ${part.part_id}`} />
                        {/if}
                        {#if artifact}
                          <StatusBadge tone="brand" label={`artifact ${artifact.artifact_id}`} />
                        {/if}
                        {#if documentItem.conversation_id}
                          <a class="button-ghost" href={`/conversations/${documentItem.conversation_id}?user_id=${encodeURIComponent(documentItem.user_id)}`}>Open conversation timeline</a>
                        {/if}
                      </div>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {:else if keyMemoryItem}
          <div class="toolbar">
            <div class="debug-block">
              <div class="sidebar-label">Key slot</div>
              <div class="message-body">
                Use this item to manage long-lived operator-approved values that can be surfaced through memory tools or admin workflows.
              </div>
            </div>
            <div class="debug-block">
              <div class="sidebar-label">Temporal indexing</div>
              {#if temporalEntries(keyMemoryItem.metadata).length === 0}
                <div class="helper-text">No temporal graph metadata is attached to this key memory.</div>
              {:else}
                <div class="list-table">
                  {#each temporalEntries(keyMemoryItem.metadata) as [key, value]}
                    <div class="table-row-link">
                      <span>{key}</span>
                      <span class="subtle-text">{value}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {/if}

        <div class="debug-block">
          <div class="sidebar-label">Audit trail</div>
          {#if audits.length === 0}
            <div class="helper-text">No audit events are recorded for this item yet.</div>
          {:else}
            <div class="toolbar">
              {#each audits as event}
                <div class="debug-block" style="background: var(--bg-panel-muted);">
                  <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px; align-items: flex-start;">
                    <div>
                      <strong>{event.action}</strong>
                      <div class="subtle-text">{event.actor_type ?? 'system'} · {event.actor_id ?? 'unknown'}</div>
                    </div>
                    <StatusBadge tone={auditTone(event.action)} label={event.created_at ? new Date(event.created_at).toLocaleString() : 'n/a'} />
                  </div>
                  <JsonView value={event.payload ?? {}} emptyLabel="No payload on this audit event." />
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
</section>
