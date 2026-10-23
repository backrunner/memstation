<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import { goto } from '$app/navigation';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();
  import { onMount } from 'svelte';

  import {
    deleteAdminKeyMemory,
    fetchConversation,
    fetchAdminKeyMemory,
    fetchAdminMemoryDocument,
    fetchAudit,
    updateAdminKeyMemory,
    updateAdminMemoryDocument
  } from '#lib/api/client.ts';
  import ChatMessageCard from '#lib/components/ChatMessageCard.svelte';
  import ConfirmActionDialog from '#lib/components/ConfirmActionDialog.svelte';
  import FactLineagePanel from '#lib/components/FactLineagePanel.svelte';
  import JsonView from '#lib/components/JsonView.svelte';
  import MemoryKeywordSignals from '#lib/components/MemoryKeywordSignals.svelte';
  import EntityAliasPanel from '#lib/components/EntityAliasPanel.svelte';
  import MemoryRelationGraph from '#lib/components/MemoryRelationGraph.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import TextareaField from '#lib/components/TextareaField.svelte';
  import { matchedKeywordsFromMetadata, sourceKeywordsFromMetadata } from '#lib/memory/keyword-signals.ts';
  import {
    documentKindFromRoute,
    memoryCollectionHref,
    normalizeMemoryRouteKind,
    type MemoryRouteKind
  } from '#lib/memory/routes.ts';
  import {
    buildSessionDraftKey,
    clearSessionDraft,
    draftsEqual,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '#lib/stores/session-drafts.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type {
    AuditEventRecord,
    ConversationRecord,
    KeyMemoryRecord,
    MemoryDocument,
    MemoryEvidence,
    MessagePart,
    MessagePartArtifact
  } from '#lib/types.ts';

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
  let deleteConfirmOpen = false;
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
    if (!draft || draftsEqual(draft, emptyDraft())) {
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

  function clearActiveDraft(): void {
    if (!activeDraftKey) {
      return;
    }
    clearSessionDraft(activeDraftKey);
    savedDraft = captureDraft();
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
      clearActiveDraft();
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
      case 'events':
        return 'Event';
      case 'tasks':
        return 'Task';
      case 'procedures':
        return 'Procedure';
      case 'artifacts':
        return 'Artifact';
      case 'entities':
        return 'Entity';
      case 'agent-actions':
        return 'Agent Action';
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
      case 'events':
        return 'Inspect durable event memories with time anchors, evidence, and graph links.';
      case 'tasks':
        return 'Review durable task state, commitments, follow-ups, and completion signals.';
      case 'procedures':
        return 'Review reusable workflows, tool practices, and operational preferences.';
      case 'artifacts':
        return 'Inspect persistent outputs or referenced resources preserved as retrievable memory.';
      case 'entities':
        return 'Inspect stable people, projects, tools, services, and organization graph anchors.';
      case 'agent-actions':
        return 'Inspect promoted agent or tool-call outcomes without retaining raw payloads.';
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
  <PageHeader
    eyebrow={`Memory / ${itemTitle}`}
    title={$page.params.id}
    description={itemDescription}
    subtitle={activeUserId ? `Scoped to user ${activeUserId}` : ''}
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect memory detail.</div>
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
        <a class="btn-ghost" href={currentCollectionHref}>Back to collection</a>
        <a class="btn-ghost" href="/memory">Memory workspace</a>
        {#if documentItem?.conversation_id}
          <a class="btn-ghost" href={`/conversations/${documentItem.conversation_id}?user_id=${encodeURIComponent(documentItem.user_id)}`}>Open source conversation</a>
        {/if}
        {#if documentItem || keyMemoryItem}
          <a class="btn-ghost" href={`/users/${encodeURIComponent((documentItem?.user_id ?? keyMemoryItem?.user_id) ?? 'admin')}`}>Open user</a>
        {/if}
      </div>
      <div class="inline-group" style="flex-wrap: wrap;">
        {#if routeKind === 'keys'}
          <Button class="btn-danger is-quiet" type="button" disabled={deleting} onclick={() => (deleteConfirmOpen = true)}>{deleting ? 'Deleting...' : 'Delete key memory'}</Button>
        {/if}
        <Button class="btn-primary" type="button" disabled={saving} onclick={save}>{saving ? 'Saving...' : 'Save changes'}</Button>
      </div>
    </div>

    <div class="grid-2" style="align-items: start;">
      <section class="card page-stack">
        <div class="page-header">
          <div>
            <h2 class="section-title">{itemTitle}</h2>
          </div>
          <div class="runtime-field-status">
            {#if documentItem}
              {#if documentItem.type === 'fact' && documentItem.lineage}
                <StatusBadge
                  tone={(documentItem.lineage.current_head_id ?? documentItem.id) === documentItem.id ? 'success' : 'warning'}
                  label={(documentItem.lineage.current_head_id ?? documentItem.id) === documentItem.id ? 'current head' : 'historical'}
                />
              {/if}
            {/if}
            {#if keyMemoryItem}
              <StatusBadge tone={reviewTone(keyMemoryItem.review_status)} label={keyMemoryItem.review_status ?? 'pending'} />
            {/if}
          </div>
        </div>

        {#if documentItem}
          {#if documentItem.type === 'entity' && $selectedTarget}<EntityAliasPanel target={$selectedTarget} document={documentItem} />{/if}
          <div class="toolbar">
            <TextareaField label="Content" bind:value={draftContent} rows={10} />
            <CollapsibleSection title="Record details">
              <div class="detail-metadata">
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
            </CollapsibleSection>

            {#if documentItem.type === 'fact'}
              <FactLineagePanel lineage={documentItem.lineage} currentFactId={documentItem.id} userId={activeUserId ?? documentItem.user_id} />
            {/if}

            <MemoryKeywordSignals
                matchedKeywords={matchedKeywordsFromMetadata(documentItem.metadata)}
                sourceKeywords={sourceKeywordsFromMetadata(documentItem.metadata)}
              />
            <CollapsibleSection title="Metadata">
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
            </CollapsibleSection>
          </div>
        {:else if keyMemoryItem}
          <div class="toolbar">
            <TextareaField label="Value" bind:value={draftValue} rows={8} />
            <CollapsibleSection title="Record details">
              <div class="detail-metadata">
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
            </CollapsibleSection>

            <div class="form-grid two-up">
              <TextInputField label="Category" bind:value={draftCategory} />
              <SelectField label="Review status" items={reviewStatusItems} bind:value={draftReviewStatus} />
            </div>

            <CollapsibleSection title="Metadata">
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
            </CollapsibleSection>
          </div>
        {/if}
      </section>

      <section class="card page-stack detail-context">
        <div class="page-header">
          <div>
            <h2 class="section-title">Related records</h2>
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
                <MemoryRelationGraph centerLabel={documentItem.id} centerType={documentItem.type} items={documentItem.related ?? []} userId={documentItem.user_id} />
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
                          <strong>{evidence.evidence_kind === 'context_reference' ? 'Context reference (not supporting evidence)' : evidence.evidence_kind}</strong>
                          {#if evidence.metadata?.support_kind}<div class="helper-text">{String(evidence.metadata.support_kind).replaceAll('_', ' ')}</div>{/if}
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
                          <a class="btn-ghost" href={`/conversations/${documentItem.conversation_id}?user_id=${encodeURIComponent(documentItem.user_id)}`}>Open conversation timeline</a>
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
                  <CollapsibleSection title="Event payload"><JsonView value={event.payload ?? {}} emptyLabel="No payload on this audit event." /></CollapsibleSection>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
</section>

<ConfirmActionDialog
  bind:open={deleteConfirmOpen}
  title="Delete key memory?"
  description="This permanently removes the key memory from active recall and memory tools."
  subject={keyMemoryItem ? `${keyMemoryItem.key} · ${keyMemoryItem.id}` : $page.params.id}
  confirmLabel="Delete key memory"
  busy={deleting}
  on:confirm={deleteKeyMemory}
/>

<style>
  .detail-context > .toolbar { gap: 0; }
  .detail-context > .toolbar > .debug-block,
  .detail-context > .debug-block { padding: 16px 0; border: 0; border-bottom: 1px solid var(--border-subtle); border-radius: 0; background: transparent; }
  .detail-context > .toolbar > .debug-block:first-child { padding-top: 0; }
  .detail-context > .debug-block:last-child { border-bottom: 0; padding-bottom: 0; }
  .detail-context .sidebar-label { display: block; margin-bottom: 8px; }
</style>
