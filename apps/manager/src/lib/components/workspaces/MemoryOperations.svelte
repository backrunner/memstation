<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { beforeNavigate, goto, invalidate } from '$app/navigation';
  import { page } from '$app/state';
  import { Dialog } from 'bits-ui';
  import { ArrowRight } from '@lucide/svelte';
  import Button from '#lib/components/ui/Button.svelte';
  import CheckboxField from '#lib/components/ui/CheckboxField.svelte';
  import CollapsibleSection from '#lib/components/ui/CollapsibleSection.svelte';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import TextareaField from '#lib/components/ui/TextareaField.svelte';
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import MetadataRow from '#lib/components/ui/MetadataRow.svelte';
  import { useSessionDraftUnloadWarning } from '#lib/stores/session-drafts.ts';
  import { latestReceipts, memoryLabel, memoryServiceView, memoryServiceViews, receiptProgress, shortMemoryID } from '#lib/memory-service-display.ts';
  import type { MemoryServiceView } from '#lib/memory-service-display.ts';
  import type { PageData } from '../../../routes/t/[target]/w/[workspace]/lifecycle/$types';
  import type { AgentRecord, CursorPage, WorkspacePage, WorkspaceRecord, SearchMemoryItem } from '#lib/types.ts';
  import type { BackgroundCall, Integration, Partition, MemoryCandidate, RecordDetail, SourceDetail, SourceRef, MemoryOperation } from '#lib/memory-service.ts';

  let { data }: { data: PageData } = $props();
  const formPrefix = $props.id();
  const abort = new AbortController();
  let alive = true;
  onDestroy(() => { alive = false; abort.abort(); });

  const workspace = $derived(data.workspaceResult.value);
  const view = $derived(memoryServiceView(page.url.searchParams.get('view')));
  const scopeBase = $derived(`/t/${encodeURIComponent(data.routeTarget.id)}/w/${encodeURIComponent(workspace?.id ?? '')}`);
  const apiBase = $derived(`/manager/api/targets/${encodeURIComponent(data.routeTarget.id)}/api/admin`);
  const root = $derived(`${apiBase}/workspaces/${encodeURIComponent(workspace?.id ?? '')}`);
  const actors = $derived(data.control.value?.actors ?? []);
  const pending = $derived((data.candidates.value?.items ?? []).filter(candidate => candidate.status === 'pending'));
  const jobs = $derived((data.control.value?.jobs ?? []).filter(job => !['complete', 'skipped'].includes(job.status)));
  const failedJobs = $derived(jobs.filter(job => ['failed', 'dead_letter'].includes(job.status)));
  const receipts = $derived(latestReceipts(data.control.value?.receipts ?? []));
  const integrations = $derived(data.integrations.value?.items ?? []);
  const partitions = $derived(data.partitions.value?.items ?? []);
  const activeView = $derived(memoryServiceViews.find(item => item.id === view)!);

  let scopeSelection = $state(untrack(() => data.partition));
  let busy = $state(false);
  let error = $state('');
  let success = $state('');
  let dialog = $state<'source' | 'candidate' | 'connection' | 'audience' | 'memory' | 'operation' | 'usage' | null>(null);
  let candidateFilter = $state('pending');
  let content = $state('');
  let kind = $state('fact');
  let subjects = $state('');
  let targetNode = $state('');
  let targetRevision = $state(0);
  let latestCorrection = $state<RecordDetail | null>(null);
  let candidateEvidence = $state<SourceRef | null>(null);
  let candidateDirty = $state(false);
  let confirmedKinds = $state<Record<string, string>>({});
  let selectedSource = $state<SourceDetail | null>(null);
  let sourceUnavailable = $state(false);
  let inspectRecord = $state<RecordDetail | null>(null);
  let recordID = $state('');
  let memoryQuery = $state('');
  let memoryResults = $state<(SearchMemoryItem & { node_id: string })[]>([]);
  let serviceNavigation = $state<HTMLElement>();
  let searched = $state(false);
  let memoryMode = $state<'inspect' | 'publish'>('inspect');
  let publicationTarget = $state('');
  let publicationPartition = $state('');
  let publicationDirty = $state(false);
  let destinationQuery = $state('');
  let foundDestinations = $state<WorkspaceRecord[] | null>(null);
  let replacement = $state('');
  let replacementRevision = $state(0);
  let preview = $state(false);
  let selectedOperation = $state<MemoryOperation | null>(null);
  let selectedCall = $state<BackgroundCall | null>(null);
  let integration = $state<Integration>(newIntegration());
  let connectionDirty = $state(false);
  let privatePartition = $state<Partition>({ id: '', agent_id: '', audience_actor_ids: [], revision: 0 });
  let audience = $state('');
  let audienceDirty = $state(false);
  let extraAgents = $state<AgentRecord[]>([]);
  let extraAgentCursor = $state<string | null>(null);
  let eraseKind = $state('source');
  let eraseID = $state('');
  let eraseRevision = $state(1);
  let eraseConfirm = $state('');
  let eraseDirty = $state(false);
  let deletionOpen = $state(false);

  const dirty = $derived(candidateDirty || connectionDirty || audienceDirty || publicationDirty || eraseDirty);
  const displayedCandidates = $derived((data.candidates.value?.items ?? []).filter(candidate => candidateFilter === 'all' || candidate.status === candidateFilter));
  const kinds = ['fact', 'preference', 'event', 'relationship', 'group_habit', 'summary', 'inference'].map(value => ({ value, label: memoryLabel(value) }));
  const directory = $derived([...new Map([...(data.agents.value?.items ?? []), ...extraAgents].map(agent => [agent.id, agent])).values()]);
  const agentCursor = $derived(extraAgentCursor ?? data.agents.value?.next_cursor ?? '');
  const agentOptions = $derived(directory.map(agent => ({ value: agent.id, label: agent.name })));
  const destinations = $derived(foundDestinations ?? data.destinations.value?.items ?? []);
  const destinationOptions = $derived(destinations.filter(item => item.id !== workspace?.id && item.status === 'active').map(item => ({ value: item.id, label: item.name })));
  const scopeOptions = $derived([{ value: '', label: 'Workspace members' }, ...partitions.map(partition => ({ value: partition.id, label: privateLabel(partition) }))]);
  const evidenceUnavailable = $derived(Boolean(candidateEvidence && data.control.value?.sources.some(source => source.id === candidateEvidence!.message_id && (source.status !== 'active' || source.revision !== candidateEvidence!.revision))));
  const viewFailures = $derived((view === 'activity' ? [data.control] : view === 'review' ? [data.candidates] : view === 'sharing' ? [data.publications] : view === 'connections' ? [data.integrations, data.partitions] : [data.control, data.generations, data.capabilities]).filter(result => result.failure));
  const dialogTitle = $derived(dialog === 'source' ? 'Source message' : dialog === 'candidate' ? targetNode ? 'Suggest a correction' : 'Add a suggestion' : dialog === 'connection' ? integration.id ? 'Edit connection' : 'Connect a source' : dialog === 'audience' ? privatePartition.id ? 'Edit private audience' : 'Create private audience' : dialog === 'memory' ? memoryMode === 'publish' ? 'Publish a memory copy' : inspectRecord ? 'Memory details' : 'Find a memory' : dialog === 'usage' ? 'AI call details' : selectedOperation?.kind === 'export' ? 'Export status' : 'Deletion status');

  useSessionDraftUnloadWarning(() => dirty);
  beforeNavigate(navigation => {
    const next = navigation.to?.url;
    const sameScope = next?.pathname === page.url.pathname && (next.searchParams.get('partition_id') ?? '') === data.partition;
    if (!navigation.willUnload && !sameScope && dirty && !window.confirm('Leave this workspace and discard unsaved memory changes?')) navigation.cancel();
  });
  $effect(() => {
    if (selectedSource && data.control.value?.sources.some(source => source.id === selectedSource!.id && (source.status !== 'active' || source.revision !== selectedSource!.revision))) {
      selectedSource = null;
      sourceUnavailable = true;
    }
    if (inspectRecord && data.candidates.value?.items.some(candidate => candidate.result_node_id === inspectRecord!.node_id && candidate.status === 'invalidated')) {
      inspectRecord = null;
      preview = false;
    }
  });

  function revealServiceSection() {
    const active = serviceNavigation?.querySelector<HTMLElement>('[aria-current]');
    if (active && serviceNavigation) {
      const left = active.offsetLeft - serviceNavigation.offsetLeft;
      if (left < serviceNavigation.scrollLeft) serviceNavigation.scrollLeft = left;
      else if (left + active.offsetWidth > serviceNavigation.scrollLeft + serviceNavigation.clientWidth) serviceNavigation.scrollLeft = left + active.offsetWidth - serviceNavigation.clientWidth;
    }
  }
  $effect(() => { void view; revealServiceSection(); });
  $effect(() => {
    if (!serviceNavigation) return;
    const observer = new ResizeObserver(revealServiceSection);
    observer.observe(serviceNavigation);
    return () => observer.disconnect();
  });

  function newIntegration(): Integration {
    return { id: '', name: '', kind: 'chat', agent_id: '', external_space_id: '', allowed_event_types: ['message.created', 'message.edited', 'message.deleted'], revision: 0 };
  }
  function split(value: string) { return value.split(',').map(part => part.trim()).filter(Boolean); }
  function actorName(id: string) { return actors.find(actor => actor.actor_id === id)?.display_name || (id ? `Actor ${shortMemoryID(id)}` : 'Unknown author'); }
  function agentName(id: string) { return directory.find(agent => agent.id === id)?.name || `Agent ${shortMemoryID(id)}`; }
  function workspaceName(id: string) { return data.destinations.value?.items.find(item => item.id === id)?.name || foundDestinations?.find(item => item.id === id)?.name || shortMemoryID(id); }
  function privateLabel(partition: Partition) { return partition.audience_actor_ids.length ? `Private: ${partition.audience_actor_ids.map(actorName).join(', ')}` : 'Private: no recipients'; }
  function people(ids: string[] = []) { return ids.length ? ids.map(actorName).join(', ') : 'Workspace'; }
  function viewLink(next: MemoryServiceView, cursorKind?: string, after?: string) {
    const query = new URLSearchParams(page.url.searchParams.toString());
    query.set('view', next);
    if (cursorKind) query.set(`${cursorKind}_after`, after ?? '');
    return `?${query}`;
  }
  function moveTo(next: MemoryServiceView) { return goto(viewLink(next), { reset: false }); }
  function updatePeople(value: string, actor: string, checked: boolean) {
    const selected = new Set(split(value));
    if (checked) selected.add(actor); else selected.delete(actor);
    return [...selected].join(', ');
  }
  function open(next: NonNullable<typeof dialog>) { error = ''; success = ''; dialog = next; }
  function close() { if (!busy) dialog = null; }
  const pendingKeys = new Map<string, string>();
  async function read<T>(url: string, init?: Parameters<typeof fetch>[1]): Promise<T> {
    const response = await fetch(url, { ...init, signal: abort.signal, cache: 'no-store' });
    if (!response.ok) throw new Error(response.status === 409 ? 'This item changed. Your draft is safe. Refresh the latest revision before retrying.' : response.status === 403 ? 'You do not have permission for this audience or operation.' : response.status === 404 ? 'This record or source is no longer available.' : `The service could not complete this request (${response.status}). Your draft is safe.`);
    const value = await response.json() as T;
    if (!alive) throw new Error('Workspace changed.');
    return value;
  }
  async function request<T>(path: string, body?: unknown, method = 'POST', management = false): Promise<T> {
    const query = new URLSearchParams({ user_id: workspace?.user_id ?? '', partition_id: data.partition });
    const payload = body === undefined ? undefined : JSON.stringify(body);
    const identity = `${method}:${root}:${data.partition}:${path}:${payload ?? ''}`;
    if (body !== undefined && !pendingKeys.has(identity)) pendingKeys.set(identity, crypto.randomUUID());
    const value = await read<T>(`${root}${management ? '' : '/lifecycle'}${path}?${query}`, { method: body === undefined ? 'GET' : method, headers: { 'Content-Type': 'application/json', ...(body !== undefined ? { 'Idempotency-Key': pendingKeys.get(identity)! } : {}) }, body: payload });
    pendingKeys.delete(identity);
    return value;
  }
  async function act(action: () => Promise<void>, message = '') {
    if (busy) return;
    busy = true; error = ''; success = '';
    try {
      await action();
      if (alive) { success = message; await invalidate('memory:lifecycle'); }
    } catch (cause) {
      if (alive) error = cause instanceof Error ? cause.message : 'Unable to complete this operation.';
    } finally { if (alive) busy = false; }
  }
  async function refresh() { await act(async () => {}, 'Status refreshed.'); }
  async function viewSource(id: string, revision?: number) {
    await act(async () => {
      const source = await request<SourceDetail>(`/sources/${encodeURIComponent(id)}`);
      if (revision !== undefined && source.revision !== revision) throw new Error('This evidence changed. Refresh the suggestions before reviewing it.');
      selectedSource = source; sourceUnavailable = false; dialog = 'source';
    });
  }
  function addSuggestion(useSource = false) {
    if (useSource && selectedSource) candidateEvidence = { workspace_id: workspace!.id, message_id: selectedSource.id, revision: selectedSource.revision, part_index: 0 };
    open('candidate');
  }
  async function propose(event: SubmitEvent) {
    event.preventDefault();
    await act(async () => {
      await request('/memory/candidates', { kind, content, partition_id: data.partition, subject_actor_ids: split(subjects), source_refs: candidateEvidence ? [candidateEvidence] : [], target_node_id: targetNode, target_revision: targetRevision });
      content = ''; subjects = ''; targetNode = ''; targetRevision = 0; latestCorrection = null; candidateEvidence = null; candidateDirty = false; dialog = null;
      await moveTo('review');
    }, 'Suggestion sent for review.');
  }
  async function decide(candidate: MemoryCandidate, decision: string) {
    await act(async () => {
      await request(`/memory/candidates/${encodeURIComponent(candidate.id)}/decision`, { expected_revision: candidate.revision, decision, ...(candidate.kind === 'inference' ? { confirmed_kind: confirmedKinds[candidate.id] } : {}) });
    }, decision === 'approve' ? 'Memory approved. Search indexing will finish in the background.' : 'Suggestion rejected.');
  }
  async function searchMemories(event: SubmitEvent) {
    event.preventDefault();
    await act(async () => { const result = await request<{ memories: SearchMemoryItem[] }>('/memory/search', { query: memoryQuery, top_n: 10 }, 'POST', true); memoryResults = result.memories.filter((record): record is SearchMemoryItem & { node_id: string } => Boolean(record.node_id)); searched = true; });
  }
  async function inspect(id = recordID) {
    await act(async () => { inspectRecord = await request<RecordDetail>(`/memory/records/${encodeURIComponent(id)}`); recordID = id; preview = false; dialog = 'memory'; });
  }
  function findMemory(mode: 'inspect' | 'publish') { memoryMode = mode; open('memory'); }
  function correct() {
    if (!inspectRecord) return;
    if (candidateDirty && !window.confirm('Replace the existing suggestion draft with this correction?')) return;
    targetNode = inspectRecord.node_id; targetRevision = inspectRecord.revision; content = inspectRecord.content; subjects = (inspectRecord.subject_actor_ids ?? []).join(', ');
    const confirmedKind = inspectRecord.kind ?? 'fact'; kind = kinds.some(item => item.value === confirmedKind) ? confirmedKind : 'fact';
    candidateEvidence = null; latestCorrection = null; candidateDirty = true; dialog = 'candidate';
  }
  async function loadCurrentCorrection() {
    await act(async () => { latestCorrection = await request<RecordDetail>(`/memory/records/${encodeURIComponent(targetNode)}`); });
  }
  function useCurrentRevision() {
    if (!latestCorrection) return;
    targetRevision = latestCorrection.revision; latestCorrection = null; error = ''; candidateDirty = true;
  }
  async function searchDestinations() {
    await act(async () => { const query = new URLSearchParams({ user_id: workspace?.user_id ?? '', q: destinationQuery, limit: '100' }); const result = await read<WorkspacePage>(`${apiBase}/workspaces?${query}`); foundDestinations = result.items; });
  }
  async function moreAgents() {
    await act(async () => { const query = new URLSearchParams({ status: 'active', limit: '100', after: agentCursor }); const result = await read<CursorPage<AgentRecord>>(`${apiBase}/users/${encodeURIComponent(workspace?.user_id ?? '')}/agents?${query}`); extraAgents = [...extraAgents, ...result.items]; extraAgentCursor = result.next_cursor; });
  }
  async function publish() {
    if (!inspectRecord) return;
    await act(async () => {
      await request('/publications', { source_node_id: inspectRecord!.node_id, source_revision: inspectRecord!.revision, target_workspace_id: publicationTarget, target_partition_id: publicationPartition, actor_mapping: {}, replaces_publication_id: replacement, expected_publication_revision: replacementRevision });
      preview = false; replacement = ''; replacementRevision = 0; publicationDirty = false; dialog = null;
      await moveTo('sharing');
    }, 'Memory copy published.');
  }
  async function reviewPublication(item: NonNullable<PageData['publications']['value']>['items'][number]) {
    replacement = item.id; replacementRevision = item.revision; publicationTarget = item.target_workspace_id; publicationPartition = item.target_partition_id ?? ''; memoryMode = 'publish';
    await inspect(item.source_node_id);
  }
  function editIntegration(value?: Integration) {
    if (connectionDirty && !window.confirm('Replace the unsaved connection draft?')) return;
    integration = value ? structuredClone(value) : newIntegration(); connectionDirty = false; open('connection');
  }
  async function saveIntegration(event: SubmitEvent) {
    event.preventDefault();
    await act(async () => { integration = await request<Integration>('/integrations', { ...integration, user_id: workspace?.user_id, workspace_id: workspace?.id }, 'PUT', true); connectionDirty = false; dialog = null; }, 'Connection saved.');
  }
  async function revokeIntegration() {
    await act(async () => { const saved = await request<Integration>('/integrations', { ...integration, user_id: workspace?.user_id, workspace_id: workspace?.id, allowed_event_types: [] }, 'PUT', true); integration = saved; connectionDirty = false; dialog = null; }, 'Message ingestion revoked.');
  }
  function editPartition(value?: Partition) {
    if (audienceDirty && !window.confirm('Replace the unsaved private audience draft?')) return;
    privatePartition = value ? structuredClone(value) : { id: '', agent_id: '', audience_actor_ids: [], revision: 0 };
    audience = privatePartition.audience_actor_ids.join(', '); audienceDirty = false; open('audience');
  }
  async function savePartition(event: SubmitEvent) {
    event.preventDefault();
    await act(async () => { privatePartition = await request<Partition>('/partitions', { ...privatePartition, user_id: workspace?.user_id, workspace_id: workspace?.id, audience_actor_ids: split(audience) }, 'PUT', true); audienceDirty = false; dialog = null; }, 'Private audience saved.');
  }
  async function operation(id: string) { await act(async () => { selectedOperation = await request<MemoryOperation>(`/operations/${encodeURIComponent(id)}`); dialog = 'operation'; }); }
  async function erase(event: SubmitEvent) {
    event.preventDefault();
    if (eraseConfirm !== workspace?.name) return;
    await act(async () => {
      const selector = { partition_id: data.partition, ...(eraseKind === 'workspace' ? { workspace: true } : eraseKind === 'actor' ? { actor_id: eraseID } : eraseKind === 'node' ? { node_ids: [eraseID] } : { source_refs: [{ workspace_id: workspace?.id, message_id: eraseID, revision: eraseRevision, part_index: 0 }] }) };
      selectedOperation = await request<MemoryOperation>('/memory/forget', selector); eraseConfirm = ''; eraseDirty = false; selectedSource = null; inspectRecord = null; preview = false; dialog = 'operation';
    }, 'Deletion accepted. Access is blocked while cleanup finishes.');
  }
  function deleteSource() {
    if (!selectedSource) return;
    eraseKind = 'source'; eraseID = selectedSource.id; eraseRevision = selectedSource.revision; eraseDirty = true; deletionOpen = true; dialog = null;
    void moveTo('data');
  }
  async function exportData() { await act(async () => { selectedOperation = await request<MemoryOperation>('/exports', {}); dialog = 'operation'; }, 'Export requested.'); }
  function download() {
    if (!selectedOperation?.result) return;
    const blob = new Blob([JSON.stringify(selectedOperation.result, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `memory-export-${workspace?.name ?? 'workspace'}.json`; anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
</script>

<section class="page-stack memory-service" aria-busy={busy}>
  <PageHeader title="Memory service" subtitle="Review memory and manage its sources.">
    <svelte:fragment slot="actions"><Button class="btn-secondary" disabled={busy} onclick={refresh}>Refresh</Button></svelte:fragment>
  </PageHeader>

  <div class="service-toolbar">
  <nav bind:this={serviceNavigation} class="service-nav" aria-label="Memory service sections">
    {#each memoryServiceViews as item}<a href={viewLink(item.id)} aria-current={view === item.id ? 'page' : undefined} data-sveltekit-reset="false">{item.label}{#if item.id === 'review' && pending.length}<span>{pending.length}{data.candidates.value?.items.length === 100 ? '+' : ''}</span>{/if}</a>{/each}
  </nav>

  <form method="GET" class="scope-bar">
    <input type="hidden" name="view" value={view} />
    <SelectField label="Audience" name="partition_id" bind:value={scopeSelection} items={scopeOptions} />
    {#if scopeSelection !== data.partition}<Button type="submit" class="btn-secondary">Apply audience</Button>{/if}
    {#if data.partition}<span class="scope-note">Private view. Access is audited.</span>{/if}
  </form>
  </div>

  {#if error && !dialog}<div class="service-feedback error-state" role="alert"><span>{error}</span><Button class="btn-secondary" disabled={busy} onclick={refresh}>Refresh latest</Button></div>{/if}
  {#if success && !dialog}<p class="service-success" role="status">{success}</p>{/if}
  {#each viewFailures as result}<p class="error-state" role="alert">{result.failure}</p>{/each}

  <section class="service-view" hidden={view !== 'activity'} aria-label="Memory activity">
    {#if data.capabilities.value?.status === 'memory-disabled'}<div class="unavailable-state"><h3>Long-term memory is unavailable</h3><p>Check the service connection and recovery status before sending more messages.</p></div>{/if}
    <div class="activity-grid">
      <section class="processing-panel" aria-labelledby="message-processing-heading">
        <div class="view-heading"><div><h2 id="message-processing-heading">Message processing</h2><p>Messages become searchable as indexing completes.</p></div></div>
      <div class="processing-list">
        {#each receipts as receipt, index (receipt.id)}
          {@const progress = receiptProgress(receipt)}
          <article class="processing-row">
            <div class="row-heading"><strong>Message stream {index + 1}</strong><span class:problem={receipt.status === 'failed'}>{memoryLabel(receipt.status === 'complete' && progress.done < progress.total ? 'processing' : receipt.status)}</span></div>
            <div class="processing-counts"><span><strong>{progress.total}</strong>Messages saved</span><span><strong>{progress.done}</strong>Searchable</span><span><strong>{Math.max(0, progress.total - progress.done)}</strong>Awaiting index</span></div>
            <progress value={progress.done} max={Math.max(1, progress.total)} aria-label={`Stream ${index + 1}: ${progress.done} of ${progress.total} events processed`}></progress>
            {#if receipt.failure_code}<p class="problem">{memoryLabel(receipt.failure_code)}{receipt.dead_letter ? '. Retry the failed task below.' : ''}</p>{/if}
            <CollapsibleSection title="Processing details"><dl class="detail-grid"><dt>Receipt</dt><dd>{receipt.id}</dd><dt>Stream</dt><dd>{receipt.stream_id}</dd><dt>Index</dt><dd>{receipt.generation_id || 'Not assigned'}</dd>{#if receipt.stage}<dt>Stage</dt><dd>{memoryLabel(receipt.stage)}</dd>{/if}</dl></CollapsibleSection>
          </article>
        {:else}<div class="empty-state"><h3>No messages received yet</h3><p>Connect a message source to start building memory.</p><a class="text-link" href={viewLink('connections')}>Set up a connection</a></div>{/each}
        {#if data.control.value?.next_cursor}<a class="text-link" href={viewLink('activity', 'control', data.control.value.next_cursor)}>More processing records</a>{/if}
      </div>
    <section class="service-block" aria-labelledby="processing-tasks-heading">
      <div class="block-heading"><h3 id="processing-tasks-heading">Background tasks</h3></div>
      {#each jobs as job (job.id)}<div class="record-row task-row" class:task-failed={['failed', 'dead_letter'].includes(job.status)}><div class="row-copy"><strong>{memoryLabel(job.task_type)}</strong><MetadataRow items={[memoryLabel(job.status), `Attempt ${job.attempt + 1}`, job.failure_code ? memoryLabel(job.failure_code) : '', job.next_retry_at ? `Retry ${new Date(job.next_retry_at).toLocaleString()}` : '']} /></div>{#if ['failed', 'dead_letter'].includes(job.status)}<Button class="btn-secondary" disabled={busy} onclick={() => act(async () => { await request(`/jobs/${encodeURIComponent(job.id)}/replay`, {}); }, 'Task queued for retry.')}>Retry task</Button>{/if}</div>{:else}<p class="quiet-empty">No tasks need attention.</p>{/each}
    </section>
      </section>
      <aside class="attention" aria-label="Needs attention">
        <div><h3>Needs attention</h3><p>Keep your workspace memory accurate and up to date.</p></div>
        <a class="attention-link" href={viewLink('review')}><div><strong>{pending.length}</strong><span>Suggestions to review</span></div><ArrowRight size={17} aria-hidden="true" /></a>
        {#if failedJobs.length}<a class="attention-link attention-failed" href="#processing-tasks-heading"><div><strong>{failedJobs.length}</strong><span>Failed tasks</span></div><ArrowRight size={17} aria-hidden="true" /></a>{:else}<p class="attention-clear">No failed tasks.</p>{/if}
        <a class="attention-footer" href={viewLink('connections')}>Manage message sources <ArrowRight size={14} aria-hidden="true" /></a>
      </aside>
    </div>
    <div class="activity-details">
    <CollapsibleSection title="Source messages" count={data.control.value?.sources.length ?? 0} variant="panel" description="Inspect original messages and authors.">
      {#each data.control.value?.sources ?? [] as source (source.id)}<div class="record-row"><div class="row-copy"><strong>{actorName(source.speaker_actor_id)}</strong><MetadataRow items={[memoryLabel(source.status), `Revision ${source.revision}`]} /></div><Button class="btn-secondary" disabled={busy || source.status !== 'active'} onclick={() => viewSource(source.id)}>View message</Button></div>{:else}<p class="quiet-empty">No source messages in this audience.</p>{/each}
    </CollapsibleSection>
    <CollapsibleSection title="AI usage" count={data.usage.value?.items.length ?? 0} variant="panel" description="Model calls and tokens for this audience.">
      {#if data.usage.failure}<p class="error-state">{data.usage.failure}</p>{/if}
      {#each data.usage.value?.items ?? [] as call (call.id)}<div class="record-row"><div class="row-copy"><strong>{memoryLabel(call.role)}</strong><MetadataRow items={[memoryLabel(call.status), `${call.input_tokens + call.output_tokens} tokens`, call.reported_cost ? `${call.reported_cost} ${call.currency}` : 'Cost not reported']} /></div><Button class="btn-secondary" onclick={() => { selectedCall = call; open('usage'); }}>Details</Button></div>{:else}<p class="quiet-empty">No AI calls recorded for this audience.</p>{/each}
      {#if data.usage.value?.items.length === 100}<a class="text-link" href={viewLink('activity', 'usage', data.usage.value.items.at(-1)!.id)}>More AI calls</a>{/if}
    </CollapsibleSection>
    </div>
  </section>

  <section class="service-view" hidden={view !== 'review'} aria-label="Memory review">
    <div class="view-heading"><div><h2>Review suggestions</h2><p>{activeView.description}</p></div><div class="actions"><Button class="btn-secondary" onclick={() => findMemory('inspect')}>Find memory</Button><Button class="btn-primary" onclick={() => addSuggestion()}>{candidateDirty ? 'Resume suggestion' : 'Add suggestion'}</Button></div></div>
    <div class="review-filter"><SelectField label="Show suggestions" bind:value={candidateFilter} items={[{ value: 'pending', label: 'Needs review' }, { value: 'all', label: 'All suggestions' }, { value: 'approved', label: 'Approved' }, { value: 'rejected', label: 'Rejected' }, { value: 'invalidated', label: 'Source changed' }]} /></div>
    <div class="suggestion-list">
      {#each displayedCandidates as candidate (candidate.id)}<article class="suggestion">
        <div class="row-heading"><MetadataRow items={[memoryLabel(candidate.kind), people(candidate.subject_actor_ids)]} />{#if candidate.status !== 'pending'}<span>{memoryLabel(candidate.status)}</span>{/if}</div>
        <p class="memory-content">{candidate.content || 'This suggestion’s source is no longer available.'}</p>
        <div class="suggestion-actions"><div class="evidence-actions">{#each candidate.source_refs ?? [] as ref, index}<Button class="btn-secondary" disabled={busy || candidate.status === 'invalidated'} onclick={() => viewSource(ref.message_id, ref.revision)}>View evidence{(candidate.source_refs ?? []).length > 1 ? ` ${index + 1}` : ''}</Button>{/each}{#if !(candidate.source_refs ?? []).length}<span class="helper-text">Human observation</span>{/if}</div>
          {#if candidate.status === 'pending'}<div class="decision-actions">{#if candidate.kind === 'inference'}<SelectField label="Record as" value={confirmedKinds[candidate.id] ?? ''} onValueChange={value => confirmedKinds[candidate.id] = value} items={kinds.filter(item => item.value !== 'inference')} placeholder="Choose type" />{/if}<Button class="btn-secondary" disabled={busy} onclick={() => decide(candidate, 'reject')}>Reject</Button><Button class="btn-primary" disabled={busy || candidate.kind === 'inference' && !confirmedKinds[candidate.id]} onclick={() => decide(candidate, 'approve')}>Approve</Button></div>{/if}
          {#if candidate.result_node_id && candidate.status === 'approved'}<Button class="btn-secondary" onclick={() => { memoryMode = 'inspect'; void inspect(candidate.result_node_id!); }}>Open memory</Button>{/if}
        </div>
      </article>{:else}<div class="empty-state"><h3>{candidateFilter === 'pending' ? 'Nothing waiting for review' : 'No matching suggestions'}</h3><p>{data.candidates.value?.items.length ? 'Try another status or the next page.' : 'New suggestions will appear here before approval.'}</p></div>{/each}
    </div>
    {#if data.candidates.value?.items.length === 100}<a class="text-link" href={viewLink('review', 'candidates', data.candidates.value.items.at(-1)!.id)}>More suggestions</a>{/if}
  </section>

  <section class="service-view" hidden={view !== 'sharing'} aria-label="Memory sharing">
    <div class="view-heading"><div><h2>Shared copies</h2><p>{activeView.description}</p></div><Button class="btn-primary" onclick={() => findMemory('publish')}>Publish memory</Button></div>
    {#each data.publications.value?.items ?? [] as publication (publication.id)}<article class="record-row"><div class="row-copy"><strong>{workspaceName(publication.target_workspace_id)}</strong><MetadataRow items={[memoryLabel(publication.status), publication.target_partition_id ? 'Private audience' : 'Workspace members']} /></div><div class="actions">{#if ['active', 'paused'].includes(publication.status)}<Button class="btn-secondary" disabled={busy} onclick={() => reviewPublication(publication)}>Review source</Button><Button class="btn-secondary" disabled={busy} onclick={() => act(async () => { await request(`/publications/${encodeURIComponent(publication.id)}/retract`, { expected_revision: publication.revision }); }, 'Shared copy retracted.')}>Retract</Button>{/if}</div></article>{:else}<div class="empty-state"><h3>No shared copies</h3><p>Publish selected memory to share it with another workspace.</p></div>{/each}
    {#if data.publications.value?.items.length === 100}<a class="text-link" href={viewLink('sharing', 'publications', data.publications.value.items.at(-1)!.id)}>More shared copies</a>{/if}
  </section>

  <section class="service-view" hidden={view !== 'connections'} aria-label="Memory connections">
    <div class="view-heading"><div><h2>Message sources</h2><p>{activeView.description}</p></div><Button class="btn-primary" onclick={() => connectionDirty ? open('connection') : editIntegration()}>{connectionDirty ? 'Resume connection' : 'Connect source'}</Button></div>
    {#each integrations as item (item.id + item.agent_id)}<div class="record-row"><div class="row-copy"><strong>{item.name}</strong><MetadataRow items={[agentName(item.agent_id), item.allowed_event_types.length ? 'Receiving messages' : 'Ingestion revoked']} /></div><Button class="btn-secondary" onclick={() => editIntegration(item)}>Edit connection</Button></div>{:else}<div class="empty-state"><h3>No sources connected</h3><p>Connect a chat or app through an authorized Agent.</p></div>{/each}
    <div class="access-link"><span>Agent permissions and service credentials</span><a class="text-link" href={`${scopeBase}/access`}>Manage Agents</a></div>
    <section class="service-block" aria-labelledby="private-audiences-heading"><div class="block-heading"><div><h3 id="private-audiences-heading">Private audiences</h3><p>Limit memory to one Agent and named recipients.</p></div><Button class="btn-secondary" onclick={() => audienceDirty ? open('audience') : editPartition()}>{audienceDirty ? 'Resume audience' : 'Create private audience'}</Button></div>
      {#each partitions as item (item.id)}<div class="record-row"><div class="row-copy"><strong>{item.audience_actor_ids.length ? people(item.audience_actor_ids) : 'No recipients'}</strong><MetadataRow items={[agentName(item.agent_id), item.audience_actor_ids.length ? 'Private' : 'Access revoked']} /></div><Button class="btn-secondary" onclick={() => editPartition(item)}>Edit audience</Button></div>{:else}<p class="quiet-empty">No private audiences configured.</p>{/each}
    </section>
  </section>

  <section class="service-view" hidden={view !== 'data'} aria-label="Memory data controls">
    <div class="view-heading"><div><h2>Data controls</h2><p>{activeView.description}</p></div></div>
    <section class="data-action"><div><h3>Export memory</h3><p>Download the selected audience’s records as JSON.</p></div><Button class="btn-secondary" disabled={busy} onclick={exportData}>Create export</Button></section>
    <CollapsibleSection title="Delete data" bind:open={deletionOpen}>
      <p class="helper-text">Deletes from the selected audience. Access is blocked before background cleanup.</p>
      <form class="page-stack compact-form" onsubmit={erase} oninput={() => eraseDirty = true}>
        <SelectField label="Delete" bind:value={eraseKind} onValueChange={() => eraseDirty = true} items={[{ value: 'source', label: 'A source message' }, { value: 'actor', label: 'A person’s contributions' }, { value: 'node', label: 'A memory record' }, { value: 'workspace', label: 'Everything in this audience' }]} />
        {#if eraseKind !== 'workspace'}<TextInputField label={eraseKind === 'source' ? 'Source message ID' : eraseKind === 'actor' ? 'Actor ID' : 'Memory node ID'} bind:value={eraseID} required />{/if}
        {#if eraseKind === 'source'}<TextInputField label="Source revision" type="number" min={1} bind:value={eraseRevision} required />{/if}
        <TextInputField label="Confirm workspace name" bind:value={eraseConfirm} required help={`Enter ${workspace?.name ?? ''} to confirm permanent deletion.`} />
        <div><Button type="submit" class="btn-danger" disabled={busy || eraseConfirm !== workspace?.name}>Delete selected data</Button></div>
      </form>
    </CollapsibleSection>
    <section class="service-block"><div class="block-heading"><h3>Recent exports and deletions</h3></div>{#each data.control.value?.operations ?? [] as operationRecord (operationRecord.id)}<div class="record-row"><div class="row-copy"><strong>{operationRecord.kind === 'export' ? 'Memory export' : 'Data deletion'}</strong><MetadataRow items={[memoryLabel(operationRecord.status), operationRecord.failure_code ? memoryLabel(operationRecord.failure_code) : '']} /></div><Button class="btn-secondary" disabled={busy} onclick={() => operation(operationRecord.id)}>View status</Button></div>{:else}<p class="quiet-empty">No exports or deletions requested.</p>{/each}</section>
    <CollapsibleSection title="Search index and AI settings">
      <div class="block-heading"><div><h3>Search index</h3><p>Rebuild all audiences in this workspace. Approved edits are preserved.</p></div><Button class="btn-secondary" disabled={busy} onclick={() => act(async () => { await request('/generations', { user_id: workspace?.user_id, scope: 'user', activate_on_success: true }); }, 'Index rebuild queued. Existing memory remains available.')}>Rebuild index</Button></div>
      {#each data.generations.value?.items ?? [] as generation (generation.generation_id)}<div class="record-row"><div class="row-copy"><strong>{generation.is_active ? 'Current index' : memoryLabel(generation.status)}</strong><MetadataRow items={[`${generation.processed_count} of ${generation.source_count} processed`, generation.failure_count ? `${generation.failure_count} failed` : '']} /></div>{#if !generation.is_active && generation.status !== 'discarded'}<Button class="btn-secondary" disabled={busy} onclick={() => act(async () => { await request(`/generations/${encodeURIComponent(generation.generation_id)}`, {}, 'DELETE'); }, 'Unused index discarded.')}>Discard index</Button>{/if}</div>{/each}
      {#if data.capabilities.value}<CollapsibleSection title="Model routing details"><dl class="detail-grid"><dt>AI connection</dt><dd>{data.capabilities.value.runtime.ai_backend === 'broker' ? 'Shared AI broker' : 'Direct providers'}</dd><dt>Usage recorded by</dt><dd>{data.capabilities.value.runtime.usage_authority}</dd>{#each Object.entries(data.capabilities.value.runtime.role_aliases) as [role, alias]}<dt>{memoryLabel(role)}</dt><dd>{alias}</dd>{/each}<dt>Embedding model</dt><dd>{data.capabilities.value.runtime.embedding.model_version || 'Deployment default'}</dd><dt>Dimensions</dt><dd>{data.capabilities.value.runtime.embedding.dimensions || 'Deployment default'}</dd><dt>Index fingerprint</dt><dd>{data.capabilities.value.runtime.embedding.fingerprint || 'Not reported'}</dd></dl></CollapsibleSection>{/if}
    </CollapsibleSection>
  </section>
</section>

<Dialog.Root open={dialog !== null} onOpenChange={value => { if (!value) close(); }}>
  <Dialog.Portal><Dialog.Overlay class="dialog-overlay" /><Dialog.Content class="dialog-card memory-dialog" escapeKeydownBehavior={busy ? 'ignore' : 'close'} interactOutsideBehavior={busy ? 'ignore' : 'close'} aria-busy={busy}>
    <div class="dialog-heading"><Dialog.Title class="section-title">{dialogTitle}</Dialog.Title><Button class="btn-ghost" disabled={busy} onclick={close}>Close</Button></div>
    <Dialog.Description class="sr-only">{dialog === 'candidate' ? 'Write a suggestion for review before it is stored as lasting memory.' : dialog === 'connection' ? 'Connect an external message source through an authorized Agent.' : dialog === 'audience' ? 'Choose the Agent and recipients allowed to use private memory.' : dialog === 'memory' ? 'Find and inspect authorized memory before correcting or publishing it.' : 'Details for the current workspace and selected audience.'}</Dialog.Description>
    <div class="memory-dialog-body">
      {#if error}<div class="error-state" role="alert">{error}<div><Button class="btn-secondary" disabled={busy} onclick={refresh}>Refresh latest</Button></div></div>{/if}
      {#if dialog === 'source'}
        {#if selectedSource}<MetadataRow items={[actorName(selectedSource.speaker_actor_id), `Revision ${selectedSource.revision}`]} /><div class="source-body">{#each selectedSource.parts as part}{#if part.type === 'quote'}<blockquote><p>{part.text}</p>{#if part.author?.actor_id}<footer>{actorName(part.author.actor_id)}</footer>{/if}</blockquote>{:else if part.type === 'media' && part.media}<MetadataRow items={[`${part.media.type} attachment`, part.media.filename, part.media.mime_type]} />{:else}<p>{part.text || (part.actor_id ? actorName(part.actor_id) : '')}</p>{/if}{/each}</div><CollapsibleSection title="Message identifiers"><dl class="detail-grid"><dt>Source message</dt><dd>{selectedSource.id}</dd><dt>Author</dt><dd>{selectedSource.speaker_actor_id || 'Unknown'}</dd></dl></CollapsibleSection>{:else}<p class="error-state">{sourceUnavailable ? 'This source has changed or been removed. Refresh before using it.' : 'Source unavailable.'}</p>{/if}
      {:else if dialog === 'candidate'}
        <form id={`${formPrefix}-candidate`} class="page-stack" onsubmit={propose} oninput={() => candidateDirty = true}>
          {#if targetNode}<div class="correction-context"><p class="form-context">Correcting revision {targetRevision}. The current fact stays available until the correction is approved.</p><div><Button class="btn-secondary" disabled={busy} onclick={loadCurrentCorrection}>Load current memory</Button></div>{#if latestCorrection}<p class="helper-text">Current revision {latestCorrection.revision}</p><p class="memory-content">{latestCorrection.content}</p><div><Button class="btn-secondary" disabled={busy} onclick={useCurrentRevision}>Use revision {latestCorrection.revision}</Button></div>{/if}</div>{/if}
          <SelectField label="Memory type" bind:value={kind} items={kinds} onValueChange={() => candidateDirty = true} />
          <TextareaField label="Suggestion" bind:value={content} required maxlength={16000} placeholder="What should this workspace remember?" />
          <CollapsibleSection title="People this concerns" description={people(split(subjects))}>{#each actors as actor}<CheckboxField label={actor.display_name || shortMemoryID(actor.actor_id)} checked={split(subjects).includes(actor.actor_id)} onCheckedChange={checked => { subjects = updatePeople(subjects, actor.actor_id, checked); candidateDirty = true; }} />{/each}<TextInputField label="Additional actor IDs" bind:value={subjects} help="Leave empty for workspace-wide memory." /></CollapsibleSection>
          {#if candidateEvidence}<div class:problem={evidenceUnavailable} class="evidence-context"><span>{evidenceUnavailable ? 'Selected evidence is no longer available.' : `Source message attached, revision ${candidateEvidence.revision}.`}</span><Button class="btn-secondary" onclick={() => { candidateEvidence = null; candidateDirty = true; }}>Remove evidence</Button></div>{:else}<p class="helper-text">Without a source message, approval is recorded as a human observation.</p>{/if}
        </form>
      {:else if dialog === 'connection'}
        <form id={`${formPrefix}-connection`} class="page-stack" onsubmit={saveIntegration} oninput={() => connectionDirty = true}>
          <TextInputField label="Connection name" bind:value={integration.name} placeholder="e.g. Team chat" required />
          <SelectField label="Observing Agent" bind:value={integration.agent_id} items={[...agentOptions, ...(integration.agent_id && !agentOptions.some(item => item.value === integration.agent_id) ? [{ value: integration.agent_id, label: agentName(integration.agent_id) }] : [])]} disabled={Boolean(integration.id)} required onValueChange={() => connectionDirty = true} />
          {#if data.agents.failure}<p class="error-state">{data.agents.failure}</p>{/if}{#if agentCursor}<div><Button class="btn-secondary" disabled={busy} onclick={moreAgents}>Load more Agents</Button></div>{/if}
          <div class="fields"><TextInputField label="Platform type" bind:value={integration.kind} readonly={Boolean(integration.id)} required /><TextInputField label="Source space ID" bind:value={integration.external_space_id} readonly={Boolean(integration.id)} required placeholder="Chat, channel or project ID" /></div>
          {#if integration.id}<p class="helper-text">Source and Agent identity are fixed. Create a new connection to change them.</p>{:else}<p class="helper-text">The Agent needs ingestion permission in this workspace. <a href={`${scopeBase}/access`}>Manage Agent access</a></p>{/if}
          <CheckboxField label="Receive message events" checked={integration.allowed_event_types.length > 0} onCheckedChange={checked => { integration.allowed_event_types = checked ? newIntegration().allowed_event_types : []; connectionDirty = true; }} />
          {#if integration.id}<CollapsibleSection title="Connection identifiers"><dl class="detail-grid"><dt>Integration</dt><dd>{integration.id}</dd><dt>Agent</dt><dd>{integration.agent_id}</dd><dt>Revision</dt><dd>{integration.revision}</dd></dl></CollapsibleSection>{/if}
        </form>
      {:else if dialog === 'audience'}
        <form id={`${formPrefix}-audience`} class="page-stack" onsubmit={savePartition} oninput={() => audienceDirty = true}>
          <SelectField label="Authorized Agent" bind:value={privatePartition.agent_id} items={[...agentOptions, ...(privatePartition.agent_id && !agentOptions.some(item => item.value === privatePartition.agent_id) ? [{ value: privatePartition.agent_id, label: agentName(privatePartition.agent_id) }] : [])]} required disabled={Boolean(privatePartition.id)} onValueChange={() => audienceDirty = true} />
          <fieldset class="people-fields"><legend>Allowed recipients</legend>{#each actors as actor}<CheckboxField label={actor.display_name || shortMemoryID(actor.actor_id)} checked={split(audience).includes(actor.actor_id)} onCheckedChange={checked => { audience = updatePeople(audience, actor.actor_id, checked); audienceDirty = true; }} />{:else}<p class="helper-text">Recipients appear after their messages are received.</p>{/each}</fieldset>
          <CollapsibleSection title="Recipient identifiers"><TextInputField label="Audience actor IDs" bind:value={audience} help="Comma-separated trusted Actor IDs." /></CollapsibleSection>
          <p class="helper-text">Only this Agent may use the memory, and only for these recipients. Group replies cannot use it.</p>
          {#if privatePartition.id && !split(audience).length}<p class="problem">Saving with no recipients revokes access to this audience.</p>{/if}
        </form>
      {:else if dialog === 'memory'}
        {#if !inspectRecord}
          <form class="search-form" onsubmit={searchMemories}><TextInputField label="Find memory" bind:value={memoryQuery} placeholder="Search by content" required /><Button type="submit" class="btn-secondary" disabled={busy}>Search</Button></form>
          {#each memoryResults as record (record.node_id)}<div class="memory-search-result"><MetadataRow items={[memoryLabel(record.type)]} /><p>{record.content}</p><div><Button class="btn-secondary" disabled={busy} onclick={() => inspect(record.node_id)}>Select memory</Button></div></div>{/each}
          {#if searched && !memoryResults.length}<p class="quiet-empty">No matching memory in this audience.</p>{/if}
          <CollapsibleSection title="Find by ID"><form class="search-form" onsubmit={event => { event.preventDefault(); void inspect(); }}><TextInputField label="Memory node ID" bind:value={recordID} required /><Button type="submit" class="btn-secondary" disabled={busy}>Load memory</Button></form></CollapsibleSection>
        {:else}
          <MetadataRow items={[memoryLabel(inspectRecord.kind ?? inspectRecord.type ?? 'Memory'), people(inspectRecord.subject_actor_ids), `Revision ${inspectRecord.revision}`]} /><p class="memory-content">{inspectRecord.content}</p>
          {#if memoryMode === 'publish'}
            {#if !preview}<form id={`${formPrefix}-publication`} class="page-stack" onsubmit={event => { event.preventDefault(); preview = true; }} oninput={() => publicationDirty = true}>
              <SelectField label="Target workspace" bind:value={publicationTarget} items={[...destinationOptions, ...(publicationTarget && !destinationOptions.some(item => item.value === publicationTarget) ? [{ value: publicationTarget, label: workspaceName(publicationTarget) }] : [])]} required onValueChange={() => publicationDirty = true} />
              <CollapsibleSection title="Find another workspace"><div class="search-form"><TextInputField label="Workspace search" bind:value={destinationQuery} placeholder="Workspace name" /><Button class="btn-secondary" disabled={busy} onclick={searchDestinations}>Find workspaces</Button></div></CollapsibleSection>
              {#if data.destinations.failure}<p class="error-state">{data.destinations.failure}</p>{/if}
              <CollapsibleSection title="Private target audience"><TextInputField label="Target private partition ID" bind:value={publicationPartition} help="Leave empty to share with the target workspace’s members." /></CollapsibleSection>
            </form>{:else}<div class="publication-preview"><h3>Confirm shared copy</h3><dl class="detail-grid"><dt>Workspace</dt><dd>{workspaceName(publicationTarget)}</dd><dt>Audience</dt><dd>{publicationPartition ? 'Selected private audience' : 'Workspace members'}</dd></dl><p class="helper-text">Shares this memory’s content. Original messages and personal profiles stay in the source workspace.</p></div>{/if}
          {:else}<CollapsibleSection title="Record identifiers"><dl class="detail-grid"><dt>Memory node</dt><dd>{inspectRecord.node_id}</dd><dt>Index</dt><dd>{inspectRecord.generation_id || 'Not assigned'}</dd><dt>Status</dt><dd>{memoryLabel(inspectRecord.status)}</dd></dl></CollapsibleSection>{/if}
        {/if}
      {:else if dialog === 'operation' && selectedOperation}
        <p class="operation-title">{memoryLabel(selectedOperation.status)}</p>
        {#if selectedOperation.kind !== 'export'}<dl class="operation-steps"><dt>Access blocked</dt><dd>{selectedOperation.read_blocked ? 'Done' : 'Pending'}</dd><dt>Live data cleaned</dt><dd>{selectedOperation.online_cleaned ? 'Done' : 'Pending'}</dd><dt>Deletion history saved</dt><dd>{selectedOperation.journal_persisted ? 'Done' : 'Pending'}</dd></dl><p class="helper-text">Older backups follow the deployment’s retention policy. Restores must apply the latest deletion history.</p>{:else}<p class="helper-text">Exports contain only the selected audience’s authorized records.{selectedOperation.expires_at ? ` Available until ${new Date(selectedOperation.expires_at).toLocaleString()}.` : ''}</p>{/if}
        {#if selectedOperation.failure_code}<p class="problem">{memoryLabel(selectedOperation.failure_code)}</p>{/if}<CollapsibleSection title="Operation identifier"><p class="mono">{selectedOperation.id}</p></CollapsibleSection>
      {:else if dialog === 'usage' && selectedCall}
        <dl class="detail-grid"><dt>Purpose</dt><dd>{memoryLabel(selectedCall.role)}</dd><dt>Result</dt><dd>{memoryLabel(selectedCall.status)}</dd><dt>Model</dt><dd>{selectedCall.model_version || selectedCall.model_alias}</dd><dt>Input tokens</dt><dd>{selectedCall.input_tokens}</dd><dt>Output tokens</dt><dd>{selectedCall.output_tokens}</dd><dt>Reported cost</dt><dd>{selectedCall.reported_cost ? `${selectedCall.reported_cost} ${selectedCall.currency}` : 'Not reported'}</dd></dl>
        <p class="helper-text">Broker usage is shown for reconciliation and is not added again to gateway billing.</p><CollapsibleSection title="Call identifiers"><dl class="detail-grid"><dt>Call</dt><dd>{selectedCall.id}</dd><dt>Broker usage</dt><dd>{selectedCall.broker_usage_id || 'Unknown'}</dd><dt>Job</dt><dd>{selectedCall.job_id || 'Search query'}</dd><dt>Model alias</dt><dd>{selectedCall.model_alias}</dd></dl></CollapsibleSection>
      {/if}
    </div>
    <div class="memory-dialog-footer">
      {#if dialog === 'candidate'}<Button class="btn-secondary" disabled={busy} onclick={close}>Keep draft</Button><Button type="submit" form={`${formPrefix}-candidate`} class="btn-primary" disabled={busy || evidenceUnavailable}>Submit suggestion</Button>
      {:else if dialog === 'connection'}{#if integration.id && integration.allowed_event_types.length}<Button class="btn-secondary revoke-action" disabled={busy} onclick={revokeIntegration}>Revoke ingestion</Button>{/if}<Button class="btn-secondary" disabled={busy} onclick={close}>Cancel</Button><Button type="submit" form={`${formPrefix}-connection`} class="btn-primary" disabled={busy}>Save connection</Button>
      {:else if dialog === 'audience'}<Button class="btn-secondary" disabled={busy} onclick={close}>Cancel</Button><Button type="submit" form={`${formPrefix}-audience`} class="btn-primary" disabled={busy || !privatePartition.id && !split(audience).length}>Save audience</Button>
      {:else if dialog === 'source' && selectedSource}<Button class="btn-secondary" disabled={busy} onclick={deleteSource}>Delete message</Button><Button class="btn-primary" disabled={busy} onclick={() => addSuggestion(true)}>Use as evidence</Button>
      {:else if dialog === 'memory' && inspectRecord}{#if memoryMode === 'publish'}{#if preview}<Button class="btn-secondary" disabled={busy} onclick={() => preview = false}>Back</Button><Button class="btn-primary" disabled={busy} onclick={publish}>Publish copy</Button>{:else}<Button class="btn-secondary" disabled={busy} onclick={() => inspectRecord = null}>Choose another</Button><Button type="submit" form={`${formPrefix}-publication`} class="btn-primary" disabled={busy}>Preview copy</Button>{/if}{:else}<Button class="btn-secondary" disabled={busy || inspectRecord.type !== 'fact'} onclick={correct}>Suggest correction</Button><Button class="btn-primary" disabled={busy} onclick={() => memoryMode = 'publish'}>Share a copy</Button>{/if}
      {:else if dialog === 'operation' && selectedOperation}<Button class="btn-secondary" disabled={busy} onclick={() => operation(selectedOperation!.id)}>Refresh status</Button>{#if selectedOperation.kind === 'export' && selectedOperation.status === 'complete' && selectedOperation.result}<Button class="btn-primary" onclick={download}>Download export</Button>{/if}
      {:else if dialog === 'usage' && selectedCall?.status === 'unknown'}<Button class="btn-primary" disabled={busy} onclick={() => act(async () => { selectedCall = await request<BackgroundCall>(`/usage/${encodeURIComponent(selectedCall!.id)}/reconcile`, {}); }, 'Usage reconciled.')}>Reconcile call</Button>
      {:else}<Button class="btn-secondary" disabled={busy} onclick={close}>Done</Button>{/if}
    </div>
  </Dialog.Content></Dialog.Portal>
</Dialog.Root>

<style>
  .memory-service { min-width: 0; gap: 20px; }
  .service-toolbar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px 20px; }
  .scope-bar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .scope-bar :global(.field) { width: 280px; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 10px; }
  .scope-bar :global(.field-label) { margin: 0; font-size: var(--font-size-xs); color: var(--text-muted); }
  .scope-note { color: var(--text-muted); font-size: var(--font-size-xs); }
  .service-nav { display: flex; gap: 4px; padding: 5px; overflow-x: auto; max-width: 100%; border: 1px solid var(--border); border-radius: 12px; background: #e5ebe8; }
  .service-nav a { display: inline-flex; align-items: center; justify-content: center; gap: 8px; flex-shrink: 0; min-height: 34px; padding: 0 14px; border-radius: 8px; color: var(--text-secondary); font-weight: 500; font-size: var(--font-size-sm); transition: color 120ms ease, background 120ms ease, box-shadow 120ms ease; }
  .service-nav a:hover { background: rgba(255, 255, 255, .55); color: var(--text); }
  .service-nav a[aria-current] { color: var(--brand-strong); background: var(--surface); box-shadow: 0 1px 4px rgba(24, 49, 35, .09); }
  .service-nav a span { display: grid; place-items: center; min-width: 18px; padding-inline: 4px; height: 18px; border-radius: 5px; background: var(--brand-soft); color: var(--brand-strong); font-size: 11px; font-variant-numeric: tabular-nums; }
  .service-view { display: grid; gap: 20px; min-width: 0; }
  .service-view[hidden] { display: none; }
  .service-view:not([aria-label='Memory activity']) { padding: 24px; border: 1px solid var(--border); border-radius: var(--radius-lg); background: var(--surface); box-shadow: var(--shadow-xs); }
  .view-heading, .block-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
  .view-heading h2 { font-size: 18px; font-weight: 600; margin: 0; letter-spacing: -.02em; }
  .view-heading p, .block-heading p, .data-action p { margin: 5px 0 0; color: var(--text-muted); font-size: var(--font-size-sm); line-height: 1.5; }
  h3 { margin: 0; font-size: var(--font-size-sm); font-weight: 600; }
  .activity-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(250px, .42fr); align-items: start; gap: 20px; }
  .processing-panel, .attention { border: 1px solid var(--border); border-radius: var(--radius-lg); background: var(--surface); box-shadow: var(--shadow-xs); }
  .processing-panel { display: grid; gap: 24px; padding: 24px; min-width: 0; }
  .processing-list { min-width: 0; }
  .processing-row { display: grid; gap: 18px; }
  .processing-row + .processing-row { border-top: 1px solid var(--border-subtle); padding-top: 20px; margin-top: 20px; }
  .processing-row :global(.disclosure-plain) { border: 0; }
  .processing-row :global(.disclosure-trigger) { min-height: 32px; padding: 4px 0; font-size: var(--font-size-xs); }
  .processing-row :global(.disclosure-affordance) { background: transparent; }
  .processing-row :global(.disclosure-body) { padding: 8px 0; }
  .row-heading { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; font-size: var(--font-size-sm); color: var(--text-secondary); }
  progress { display: block; width: 100%; height: 7px; border: 0; border-radius: 4px; overflow: hidden; background: var(--surface-elevated); accent-color: var(--brand); }
  progress::-webkit-progress-bar { background: var(--surface-elevated); }
  progress::-webkit-progress-value { background: var(--brand); border-radius: 4px; }
  progress::-moz-progress-bar { background: var(--brand); border-radius: 4px; }
  .processing-counts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 20px; color: var(--text-muted); font-size: var(--font-size-xs); }
  .processing-counts span { display: grid; gap: 4px; }
  .processing-counts strong { color: var(--text); font-size: 30px; line-height: 1.15; letter-spacing: -.04em; font-weight: 500; font-variant-numeric: tabular-nums; }
  .attention { display: grid; align-content: start; gap: 20px; padding: 24px; }
  .attention h3 { font-size: 15px; letter-spacing: -.01em; }
  .attention p { margin: 6px 0 0; color: var(--text-muted); line-height: 1.6; font-size: var(--font-size-xs); }
  .attention-link { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding-block: 0 18px; border-bottom: 1px solid var(--border-subtle); color: var(--brand-strong); }
  .attention-link div { display: grid; gap: 6px; }
  .attention-link strong { font-size: 28px; line-height: 1.1; font-weight: 500; font-variant-numeric: tabular-nums; }
  .attention-link span { font-size: var(--font-size-sm); }
  .attention-link:hover span, .attention-footer:hover { text-decoration: underline; text-underline-offset: 3px; }
  .attention-failed { color: var(--danger); }
  .attention-footer { display: flex; justify-content: space-between; align-items: center; gap: 10px; color: var(--text-secondary); font-size: var(--font-size-xs); }
  .activity-details { display: grid; grid-template-columns: 1fr 1fr; align-items: start; gap: 20px; }
  .activity-details :global(.disclosure-panel) { border-radius: var(--radius-md); box-shadow: var(--shadow-xs); }
  .activity-details :global(.disclosure-trigger) { padding: 18px 20px; }
  .task-row:last-child { border-bottom: 0; padding-bottom: 0; }
  .task-failed { margin-top: 12px; padding: 12px !important; border: 1px solid #f1ded5 !important; border-radius: 10px; background: #fdf8f5; }
  #processing-tasks-heading { scroll-margin-top: calc(var(--topbar-height) + var(--shell-inset) * 2 + 20px); }
  .service-block { border-top: 1px solid var(--border-subtle); padding-top: 20px; }
  .record-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 14px 0; border-bottom: 1px solid var(--border-subtle); font-size: var(--font-size-sm); }
  .row-copy { min-width: 0; display: grid; gap: 4px; overflow-wrap: anywhere; }
  .record-row strong { font-weight: 500; }
  .actions, .evidence-actions, .decision-actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
  .service-feedback { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
  .service-success { margin: 0; font-size: var(--font-size-sm); color: var(--brand-strong); padding: 9px 12px; background: var(--brand-softer); border-left: 2px solid var(--brand); }
  .empty-state { padding: 30px 0; display: grid; gap: 8px; }
  .empty-state p, .quiet-empty { margin: 0; color: var(--text-muted); font-size: var(--font-size-sm); line-height: 1.5; }
  .quiet-empty { padding: 16px 0; }
  .review-filter { width: min(100%, 220px); }
  .suggestion { padding: 18px 0; display: grid; gap: 12px; border-top: 1px solid var(--border-subtle); }
  .memory-content { margin: 0; color: var(--text); white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.65; font-size: var(--font-size-sm); }
  .suggestion-actions { display: flex; justify-content: space-between; align-items: end; flex-wrap: wrap; gap: 12px; }
  .decision-actions :global(.field) { min-width: 170px; }
  .access-link { display: flex; justify-content: space-between; gap: 12px; color: var(--text-muted); font-size: var(--font-size-sm); padding: 8px 0 20px; }
  .data-action { display: flex; justify-content: space-between; gap: 16px; align-items: center; padding: 0 0 12px; }
  .compact-form { max-width: 560px; }
  .problem { margin: 0; color: var(--danger, #b42318); font-size: var(--font-size-sm); }
  .unavailable-state { border-left: 3px solid var(--danger, #b42318); padding: 12px 16px; background: var(--surface-muted); }
  .unavailable-state p { color: var(--text-muted); font-size: var(--font-size-sm); }
  :global(.memory-dialog) { display: flex; flex-direction: column; width: min(640px, calc(100vw - 32px)); padding: 0; overflow: hidden; }
  .dialog-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 16px 20px; border-bottom: 1px solid var(--border); flex-shrink: 0; }
  .memory-dialog-body { display: grid; gap: 16px; overflow-y: auto; min-height: 0; padding: 20px; }
  .memory-dialog-footer { display: flex; justify-content: end; align-items: center; gap: 8px; flex-wrap: wrap; padding: 14px 20px; border-top: 1px solid var(--border); background: var(--surface); flex-shrink: 0; }
  .memory-dialog-footer :global(.revoke-action) { margin-right: auto; }
  .fields { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  .people-fields { border: 0; padding: 0; margin: 0; }
  .people-fields legend { margin-bottom: 8px; font-size: var(--font-size-sm); font-weight: 500; }
  .detail-grid, .operation-steps { display: grid; grid-template-columns: minmax(110px, .34fr) minmax(0, 1fr); gap: 12px 18px; margin: 0; font-size: var(--font-size-sm); }
  .detail-grid dt, .operation-steps dt { color: var(--text-muted); }
  .detail-grid dd, .operation-steps dd { margin: 0; overflow-wrap: anywhere; }
  .detail-grid dd { font-family: var(--font-mono); font-size: var(--font-size-xs); }
  .operation-title { margin: 0; font-size: 18px; font-weight: 600; }
  .source-body { display: grid; gap: 14px; line-height: 1.7; font-size: var(--font-size-sm); overflow-wrap: anywhere; }
  .source-body p { margin: 0; white-space: pre-wrap; }
  blockquote { border-left: 2px solid var(--border-strong); margin: 0; padding: 4px 12px; color: var(--text-secondary); }
  blockquote footer { margin-top: 6px; color: var(--text-muted); font-size: var(--font-size-xs); }
  .search-form { display: flex; align-items: end; gap: 8px; }
  .search-form :global(.field) { flex: 1; min-width: 0; }
  .memory-search-result { border-bottom: 1px solid var(--border-subtle); padding-bottom: 16px; display: grid; gap: 8px; }
  .memory-search-result p { margin: 0; font-size: var(--font-size-sm); overflow-wrap: anywhere; }
  .correction-context { display: grid; gap: 10px; }
  .form-context { padding: 10px 12px; margin: 0; color: var(--text-secondary); background: var(--surface-muted); font-size: var(--font-size-sm); }
  .evidence-context { display: flex; justify-content: space-between; align-items: center; gap: 12px; font-size: var(--font-size-sm); }
  .publication-preview { display: grid; gap: 16px; }
  .mono { overflow-wrap: anywhere; }
  @media (max-width: 900px) {
    .activity-grid { grid-template-columns: 1fr; gap: 16px; }
    .attention { grid-template-columns: 1fr 1fr; gap: 18px; }
    .attention > div, .attention-footer, .attention-clear { grid-column: 1 / -1; }
    .attention-link { padding-bottom: 0; border-bottom: 0; }
    .service-toolbar { align-items: stretch; }
    .activity-details { gap: 16px; }
  }
  @media (max-width: 600px) {
    .memory-service :global(.page-header) { flex-wrap: nowrap; align-items: start; }
    .memory-service :global(.page-header-meta) { flex: 1; }
    .service-toolbar { flex-direction: column; gap: 14px; }
    .service-nav { gap: 2px; width: 100%; padding: 4px; }
    .service-nav a { min-height: 44px; padding-inline: 11px; }
    .scope-bar :global(.field) { flex: 1; min-width: 0; width: auto; grid-template-columns: auto minmax(0, 1fr); }
    .scope-note { width: 100%; }
    .processing-panel, .attention { padding: 20px; }
    .service-view:not([aria-label='Memory activity']) { padding: 20px 16px; }
    .activity-details { grid-template-columns: 1fr; }
    .processing-counts { gap: 12px; }
    .processing-counts strong { font-size: 28px; }
    .view-heading, .block-heading { align-items: start; flex-direction: column; gap: 12px; }
    .view-heading .actions { width: 100%; }
    .record-row { align-items: start; flex-wrap: wrap; gap: 10px; }
    .record-row .row-copy { flex: 1; min-width: 160px; }
    .processing-counts { gap: 16px; }
    .processing-row :global(.disclosure-trigger) { min-height: 44px; }
    .suggestion-actions { align-items: stretch; }
    .decision-actions { margin-left: auto; }
    .decision-actions :global(.field) { width: 100%; }
    .access-link, .data-action { flex-direction: column; align-items: start; }
    .fields { grid-template-columns: 1fr; }
    .dialog-heading, .memory-dialog-body { padding: 16px; }
    .memory-dialog-footer { padding: 12px 16px; }
    .detail-grid { grid-template-columns: minmax(80px, .38fr) minmax(0, 1fr); gap: 10px; }
    .evidence-context { align-items: start; flex-direction: column; }
  }
</style>
