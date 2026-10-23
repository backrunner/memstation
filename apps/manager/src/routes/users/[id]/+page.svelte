<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import { onDestroy, onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  import {
    createMemoryRebuild,
    createUserAPIKey,
    fetchMemoryRebuildDetail,
    fetchUserDetail,
    fetchUserSources,
    revokeUserAPIKey,
    updateUserSettings
  } from '#lib/api/client.ts';
  import CodeBlock from '#lib/components/CodeBlock.svelte';
  import ConfirmActionDialog from '#lib/components/ConfirmActionDialog.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import SwitchField from '#lib/components/SwitchField.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { isNonNegativeInteger } from '#lib/forms/validation.ts';
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
  import type { KeyMemoryRecord, MemoryGeneration, MemoryRebuildDetailPayload, UserDetailPayload, UserSourcesPayload } from '#lib/types.ts';

  type UserDetailDraft = {
    newKeyName: string;
    chatModel: string;
    summaryModel: string;
    analysisModel: string;
    embeddingModel: string;
    toolWriteEnabled: boolean;
    retainSourceMaterials: boolean;
    maxInjectionTokens: number;
  };

  const userDetailDraftVersion = 1;

  let loading = true;
  let saving = false;
  let creatingKey = false;
  let error = '';
  let success = '';
  let lastTargetKey = '';
  let detail: UserDetailPayload | null = null;
  let sources: UserSourcesPayload | null = null;
  let createdKey = '';
  let newKeyName = '';
  let chatModel = '';
  let summaryModel = '';
  let analysisModel = '';
  let embeddingModel = '';
  let toolWriteEnabled = false;
  let retainSourceMaterials = false;
  let maxInjectionTokens = 0;
  let rebuildRequired = false;
  let rebuilding = false;
  let revokeCandidateId = '';
  let rebuildConfirmOpen = false;
  let fieldErrors: Record<string, string> = {};
  let latestRebuild: MemoryRebuildDetailPayload | null = null;
  let rebuildPollTimer: ReturnType<typeof setTimeout> | null = null;
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let savedDraft = emptyDraft();
  let currentDraft = emptyDraft();

  onMount(async () => {
    await loadDetail();
  });

  onDestroy(() => {
    clearRebuildPoll();
  });

  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${$page.params.id}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: userDetailDraftKey = buildSessionDraftKey('users.detail', $selectedTarget?.id, $page.params.id);
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadDetail();
  }
  $: currentDraft = captureDraft();
  $: if (draftReady && activeDraftKey && !hydratingDraft) {
    if (draftsEqual(currentDraft, savedDraft)) {
      clearSessionDraft(activeDraftKey);
    } else {
      writeSessionDraft(activeDraftKey, userDetailDraftVersion, currentDraft);
    }
  }

  useSessionDraftUnloadWarning(
    () => draftReady && !draftsEqual(currentDraft, savedDraft),
    'This user page has unsaved settings or key input. Closing or reloading the page may discard the draft kept in this tab.'
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
    writeSessionDraft(activeDraftKey, userDetailDraftVersion, snapshot);
  });

  function emptyDraft(): UserDetailDraft {
    return {
      newKeyName: '',
      chatModel: '',
      summaryModel: '',
      analysisModel: '',
      embeddingModel: '',
      toolWriteEnabled: false,
      retainSourceMaterials: false,
      maxInjectionTokens: 0
    };
  }

  function captureDraft(): UserDetailDraft {
    return {
      newKeyName,
      chatModel,
      summaryModel,
      analysisModel,
      embeddingModel,
      toolWriteEnabled,
      retainSourceMaterials,
      maxInjectionTokens
    };
  }

  function baselineDraft(): UserDetailDraft {
    return {
      newKeyName: '',
      chatModel: String(detail?.settings?.chat_model ?? ''),
      summaryModel: String(detail?.settings?.summary_model ?? ''),
      analysisModel: String(detail?.settings?.analysis_model ?? ''),
      embeddingModel: String(detail?.settings?.embedding_model ?? ''),
      toolWriteEnabled: Boolean(detail?.settings?.tool_write_enabled),
      retainSourceMaterials: Boolean(detail?.settings?.retain_source_materials),
      maxInjectionTokens: Number(detail?.settings?.max_injection_tokens ?? 0)
    };
  }

  function applyDraft(draft: UserDetailDraft | null): void {
    if (!draft) {
      return;
    }
    newKeyName = draft.newKeyName ?? '';
    chatModel = draft.chatModel ?? chatModel;
    summaryModel = draft.summaryModel ?? summaryModel;
    analysisModel = draft.analysisModel ?? analysisModel;
    embeddingModel = draft.embeddingModel ?? embeddingModel;
    toolWriteEnabled = draft.toolWriteEnabled ?? toolWriteEnabled;
    retainSourceMaterials = draft.retainSourceMaterials ?? retainSourceMaterials;
    maxInjectionTokens = draft.maxInjectionTokens ?? maxInjectionTokens;
  }

  function syncForm(): void {
    chatModel = String(detail?.settings?.chat_model ?? '');
    summaryModel = String(detail?.settings?.summary_model ?? '');
    analysisModel = String(detail?.settings?.analysis_model ?? '');
    embeddingModel = String(detail?.settings?.embedding_model ?? '');
    toolWriteEnabled = Boolean(detail?.settings?.tool_write_enabled);
    retainSourceMaterials = Boolean(detail?.settings?.retain_source_materials);
    maxInjectionTokens = Number(detail?.settings?.max_injection_tokens ?? 0);
  }

  function clearRebuildPoll(): void {
    if (rebuildPollTimer) {
      clearTimeout(rebuildPollTimer);
      rebuildPollTimer = null;
    }
  }

  function latestGenerationCandidate(): MemoryGeneration | null {
    const generations = [...(sources?.generations ?? [])];
    if (generations.length === 0) {
      return null;
    }
    generations.sort((left, right) => {
      const leftPending = left.status === 'queued' || left.status === 'running' ? 1 : 0;
      const rightPending = right.status === 'queued' || right.status === 'running' ? 1 : 0;
      if (leftPending !== rightPending) {
        return rightPending - leftPending;
      }
      const leftTime = new Date(left.updated_at ?? left.created_at ?? 0).getTime();
      const rightTime = new Date(right.updated_at ?? right.created_at ?? 0).getTime();
      return rightTime - leftTime;
    });
    return generations[0] ?? null;
  }

  async function syncLatestRebuild(): Promise<void> {
    clearRebuildPoll();
    if (!$selectedTarget || !$page.params.id) {
      latestRebuild = null;
      return;
    }
    const candidate = latestGenerationCandidate();
    if (!candidate) {
      latestRebuild = null;
      return;
    }
    try {
      latestRebuild = await fetchMemoryRebuildDetail($selectedTarget, $page.params.id, candidate.generation_id);
      if (latestRebuild.item.status === 'queued' || latestRebuild.item.status === 'running') {
        rebuildPollTimer = setTimeout(() => {
          void syncLatestRebuild();
        }, 1500);
      }
    } catch {
      latestRebuild = {
        item: candidate,
        sources: []
      };
    }
  }

  async function loadDetail(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !$page.params.id) {
      loading = false;
      draftReady = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      [detail, sources] = await Promise.all([fetchUserDetail($selectedTarget, $page.params.id), fetchUserSources($selectedTarget, $page.params.id)]);
      rebuildRequired = false;
      if (sources?.active_generation && detail?.settings?.embedding_model) {
        rebuildRequired = String(detail.settings.embedding_model ?? '') !== sources.active_generation.embedding_model;
      }
      syncForm();
      savedDraft = baselineDraft();
      activeDraftKey = userDetailDraftKey;
      hydratingDraft = true;
      applyDraft(readSessionDraft<UserDetailDraft>(userDetailDraftKey, userDetailDraftVersion));
      hydratingDraft = false;
      draftReady = true;
      await syncLatestRebuild();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load user detail';
    } finally {
      loading = false;
    }
  }

  async function saveSettings(): Promise<void> {
    if (!$selectedTarget || !$page.params.id) {
      return;
    }
    fieldErrors = {};
    if (!isNonNegativeInteger(maxInjectionTokens)) {
      fieldErrors.maxInjectionTokens = 'Use a non-negative integer.';
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const payload: Record<string, unknown> = {
        chat_model: chatModel,
        summary_model: summaryModel,
        analysis_model: analysisModel,
        embedding_model: embeddingModel,
        retain_source_materials: retainSourceMaterials,
        tool_write_enabled: toolWriteEnabled,
        max_injection_tokens: maxInjectionTokens
      };
      const response = await updateUserSettings($selectedTarget, $page.params.id, payload);
      detail = detail ? { ...detail, settings: response.settings } : detail;
      sources = await fetchUserSources($selectedTarget, $page.params.id);
      rebuildRequired = Boolean(response.memory_rebuild_required) || (sources?.active_generation ? String(response.settings.embedding_model ?? '') !== sources.active_generation.embedding_model : false);
      syncForm();
      savedDraft = baselineDraft();
      await syncLatestRebuild();
      success = rebuildRequired
        ? 'User settings saved. A memory rebuild is now recommended before the new generation becomes active.'
        : 'User settings saved directly to the selected target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save settings';
    } finally {
      saving = false;
    }
  }

  async function createKey(): Promise<void> {
    if (!$selectedTarget || !$page.params.id) {
      return;
    }
    creatingKey = true;
    error = '';
    success = '';
    createdKey = '';
    try {
      const payload = await createUserAPIKey($selectedTarget, $page.params.id, newKeyName.trim() || 'manager-created');
      createdKey = payload.api_key;
      newKeyName = '';
      if (activeDraftKey) {
        writeSessionDraft(activeDraftKey, userDetailDraftVersion, captureDraft());
      }
      await loadDetail();
      success = 'API key created. The raw key is shown once below.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to create API key';
    } finally {
      creatingKey = false;
    }
  }

  async function revokeKey(keyId: string): Promise<void> {
    if (!$selectedTarget || !$page.params.id) {
      return;
    }
    error = '';
    success = '';
    try {
      await revokeUserAPIKey($selectedTarget, $page.params.id, keyId);
      await loadDetail();
      success = 'API key revoked.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to revoke API key';
    }
  }

  async function rebuildAllRetainedMemory(): Promise<void> {
    if (!$selectedTarget || !$page.params.id) {
      return;
    }
    rebuilding = true;
    error = '';
    success = '';
    try {
      latestRebuild = await createMemoryRebuild($selectedTarget, {
        userId: $page.params.id,
        scope: 'user',
        activateOnSuccess: true
      });
      sources = await fetchUserSources($selectedTarget, $page.params.id);
      await syncLatestRebuild();
      success = 'Rebuild generation created. The worker is replaying retained snapshots now.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to create memory rebuild';
    } finally {
      rebuilding = false;
    }
  }

  function generationTone(item: MemoryGeneration): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (item.is_active) {
      return 'success';
    }
    if (item.status === 'failed') {
      return 'danger';
    }
    if (item.status === 'queued' || item.status === 'running') {
      return 'warning';
    }
    return 'muted';
  }

  function reviewTone(reviewStatus?: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (reviewStatus === 'approved') {
      return 'success';
    }
    if (reviewStatus === 'rejected') {
      return 'danger';
    }
    if (!reviewStatus) {
      return 'muted';
    }
    return 'warning';
  }

  function formatDateTime(value?: string): string {
    return value ? new Date(value).toLocaleString() : 'Not recorded';
  }

  function keyMemorySummary(value?: string): string {
    const trimmed = String(value ?? '').trim();
    if (!trimmed) {
      return 'No stable value has been stored for this slot yet.';
    }
    return trimmed.length > 220 ? `${trimmed.slice(0, 217)}...` : trimmed;
  }

  function keyMemoryFacts(item: KeyMemoryRecord): Array<{ label: string; value: string }> {
    const facts = [
      item.source ? { label: 'Source', value: item.source } : null,
      item.last_confirmed_at ? { label: 'Last confirmed', value: formatDateTime(item.last_confirmed_at) } : null,
      item.activity_at ? { label: 'Activity', value: formatDateTime(item.activity_at) } : null,
      item.updated_at ? { label: 'Updated', value: formatDateTime(item.updated_at) } : null,
      item.importance !== undefined ? { label: 'Importance', value: item.importance.toFixed(3) } : null,
      item.effective_score !== undefined ? { label: 'Effective score', value: item.effective_score.toFixed(3) } : null
    ];
    return facts.filter((entry): entry is { label: string; value: string } => Boolean(entry));
  }

  function keyMemoryMeta(item: KeyMemoryRecord): string {
    const parts = [
      item.source ?? '',
      item.category ?? '',
      item.updated_at ? formatDateTime(item.updated_at) : ''
    ].filter(Boolean);
    return parts.join(' · ');
  }

  function conversationStatusTone(status: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (status === 'ended') {
      return 'success';
    }
    if (status === 'failed') {
      return 'danger';
    }
    if (status === 'queued' || status === 'running') {
      return 'warning';
    }
    return 'muted';
  }

  $: rebuildInFlight = latestRebuild ? latestRebuild.item.status === 'queued' || latestRebuild.item.status === 'running' : false;
  $: rebuildProgressLabel = latestRebuild ? `${latestRebuild.item.processed_count}/${latestRebuild.item.source_count} snapshots` : 'No rebuild yet';
  $: rebuildFailedSources = latestRebuild ? latestRebuild.sources.filter((source) => source.status === 'failed').length : 0;
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="User"
    title={$page.params.id}
    description="API keys, settings, and memory generation state for this user."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect user detail pages.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading user detail…</div>
  {:else if detail}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}
    {#if createdKey}
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h3 class="section-title">New API key</h3>
            <p class="panel-meta">Shown once — copy and store now.</p>
          </div>
        </div>
        <CodeBlock code={createdKey} language="text" />
      </section>
    {/if}

    <div class="kpi-grid summary-metrics compact-kpis">
      <div class="kpi-card">
        <span class="kpi-label">API Keys</span>
        <strong class="kpi-value">{detail.stats.api_key_count}</strong>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Sessions</span>
        <strong class="kpi-value">{detail.stats.conversation_count}</strong>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Key Memory</span>
        <strong class="kpi-value">{detail.stats.key_memory_count}</strong>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Total Tokens</span>
        <strong class="kpi-value">{detail.token_usage.total_tokens.toLocaleString()}</strong>
      </div>
    </div>

    <div class="page-columns user-detail-columns">
      <div class="page-column">
        <section class="card">
        <div class="page-header">
          <div>
            <h2 class="section-title">Identity And Settings</h2>
            {#if detail.user.is_admin}
              <div class="helper-text" style="margin-top: 4px;">Admin User</div>
            {/if}
          </div>
        </div>

        <div class="profile-strip">
          <div>
            <div class="sidebar-label">Name</div>
            <strong>{detail.user.name}</strong>
          </div>
          <div>
            <div class="sidebar-label">Created</div>
            <span>{new Date(detail.user.created_at).toLocaleString()}</span>
          </div>
          {#if detail.user.email}
            <div>
              <div class="sidebar-label">Email</div>
              <span>{detail.user.email}</span>
            </div>
          {/if}
        </div>

        <div class="form-grid two-up" style="margin-top: 12px;">
          <TextInputField label="Chat Model" bind:value={chatModel} />
          <TextInputField label="Summary Model" bind:value={summaryModel} />
          <TextInputField label="Analysis Model" bind:value={analysisModel} />
          <TextInputField label="Embedding Model" bind:value={embeddingModel} />
          <SwitchField label="Tool Writes" bind:checked={toolWriteEnabled} />
          <SwitchField label="Source Retention" bind:checked={retainSourceMaterials} />
          <TextInputField
            label="Max Injection Tokens"
            bind:value={maxInjectionTokens}
            type="number"
            min="0"
            error={fieldErrors.maxInjectionTokens}
          />
        </div>

        <div class="form-actions" style="margin-top: 16px;">
          <Button class="btn-primary" type="button" disabled={saving} onclick={saveSettings}>{saving ? 'Saving...' : 'Save User Settings'}</Button>
        </div>
        </section>

        <section class="card">
          <div class="page-header">
            <div>
              <h2 class="section-title">API Keys</h2>
              <p class="panel-meta">Creation and revocation are audited.</p>
            </div>
          </div>
          <div class="inline-group" style="align-items: end; margin-bottom: 14px;">
            <div style="flex: 1; min-width: 0;">
              <TextInputField label="New Key Label" bind:value={newKeyName} placeholder="build-bot or local playground" />
            </div>
            <Button class="btn-primary" type="button" disabled={creatingKey} onclick={createKey}>{creatingKey ? 'Creating...' : 'Create API Key'}</Button>
          </div>

          <div class="toolbar">
            {#if detail.api_keys.length === 0}
              <div class="empty-state">No API keys have been created for this user yet.</div>
            {:else}
              {#each detail.api_keys as apiKey}
                <div class="debug-block">
                  <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                    <div>
                      <strong>{apiKey.name}</strong>
                      <div class="subtle-text">{apiKey.prefix}</div>
                    </div>
                    <StatusBadge tone={apiKey.revoked_at ? 'danger' : 'success'} label={apiKey.revoked_at ? 'Revoked' : 'Active'} />
                  </div>
                  <div class="helper-text">Created {new Date(apiKey.created_at).toLocaleString()}</div>
                  {#if !apiKey.revoked_at}
                    <div class="inline-group" style="justify-content: flex-end; margin-top: 12px;">
                      <Button class="btn-danger is-quiet" type="button" onclick={() => (revokeCandidateId = apiKey.id)}>Revoke key</Button>
                    </div>
                  {/if}
                </div>
              {/each}
            {/if}
          </div>
        </section>
      </div>

      <div class="page-column">
        <section class="card">
        <div class="page-header">
          <div>
            <h2 class="section-title">Retention And Rebuild</h2>
          </div>
        </div>
        <div class="toolbar">
          <div class="debug-block">
            <div class="sidebar-label">Latest snapshot</div>
            <div>{sources?.source_stats.latest_snapshot_at ? new Date(sources.source_stats.latest_snapshot_at).toLocaleString() : 'None captured yet'}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Active generation</div>
            <div class="inline-group">
              <strong>{sources?.active_generation.generation_id ?? 'Unavailable'}</strong>
              {#if sources?.active_generation}
                <StatusBadge tone={generationTone(sources.active_generation)} label={sources.active_generation.status} />
              {/if}
            </div>
            {#if sources?.active_generation}
              <div class="helper-text">Embedding v{sources.active_generation.embedding_version} · {sources.active_generation.embedding_model}</div>
            {/if}
          </div>
        </div>
        {#if latestRebuild}
          <div class="debug-block" style="margin-top: 12px;">
            <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
              <strong>Latest rebuild</strong>
              <StatusBadge tone={generationTone(latestRebuild.item)} label={latestRebuild.item.status} />
            </div>
            <div class="helper-text">{latestRebuild.item.generation_id}</div>
            <div class="helper-text" style="margin-top: 6px;">{rebuildProgressLabel} · {latestRebuild.item.embedding_count} embeddings</div>
            {#if rebuildFailedSources > 0}
              <div class="field-error" style="margin-top: 6px;">{rebuildFailedSources} retained source{rebuildFailedSources === 1 ? '' : 's'} failed during replay.</div>
            {/if}
          </div>
        {/if}
        {#if rebuildRequired}
          <div class="notice-card" style="margin-top: 12px;">Current settings differ from the active memory generation. Trigger a rebuild to replay retained sources with the new configuration.</div>
        {/if}
        <div class="inline-group" style="justify-content: flex-end; margin-top: 12px;">
          <Button class="btn-primary" type="button" disabled={rebuilding || rebuildInFlight || (sources?.source_stats.snapshot_count ?? 0) === 0} onclick={() => (rebuildConfirmOpen = true)}>
            {rebuilding ? 'Creating rebuild...' : rebuildInFlight ? 'Rebuild running...' : 'Rebuild all retained memory'}
          </Button>
        </div>
        <div class="generation-list" style="margin-top: 12px;">
          {#if (sources?.generations.length ?? 0) === 0}
            <div class="empty-state">No memory generations are available for this user yet.</div>
          {:else}
            {#each sources?.generations ?? [] as generation}
              <div class="debug-block generation-card">
                <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                  <div>
                    <strong>{generation.scope}</strong>
                    <div class="subtle-text">{generation.processed_count}/{generation.source_count} snapshots · {generation.embedding_count} embeddings</div>
                  </div>
                  <StatusBadge tone={generationTone(generation)} label={generation.is_active ? 'active' : generation.status} />
                </div>
                <div class="helper-text">{generation.generation_id}</div>
              </div>
            {/each}
          {/if}
        </div>
        </section>

        <section class="card page-stack">
          <div class="page-header">
            <div>
              <h2 class="section-title">Recent Sessions</h2>
            </div>
            <a class="btn-ghost" href={`/memory?user_id=${detail.user.id}`}>Open memory workspace</a>
          </div>
          {#if detail.conversations.length === 0}
            <div class="empty-state">No recent sessions are available for this user yet.</div>
          {:else}
            <div class="session-list">
              {#each detail.conversations as conversation}
                <a class="session-row" href={`/conversations/${conversation.conversation_id}?user_id=${detail.user.id}`}>
                  <div class="session-row-main">
                    <strong>{conversation.conversation_id}</strong>
                    <div class="subtle-text">{conversation.protocol} · {conversation.mode}</div>
                  </div>
                  <div class="session-row-side">
                    <span class="session-provider">{conversation.provider}</span>
                    <StatusBadge tone={conversationStatusTone(conversation.finalize_status)} label={conversation.finalize_status} />
                    <span class="session-tokens">{conversation.total_tokens} tokens</span>
                  </div>
                </a>
              {/each}
            </div>
          {/if}
        </section>

        <section class="card">
          <div class="page-header">
            <div>
              <h2 class="section-title">Stable Memory Slots</h2>
            </div>
            <a class="btn-ghost" href={`/memory/keys?user_id=${detail.user.id}`}>Manage key memories</a>
          </div>
          {#if detail.key_memories.length === 0}
            <div class="empty-state">This user does not have any key memories yet.</div>
          {:else}
            <div class="toolbar key-memory-stack">
              {#each detail.key_memories as keyMemory}
                <div class="debug-block key-memory-card">
                  <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                    <div>
                      <strong>{keyMemory.key}</strong>
                      {#if keyMemoryMeta(keyMemory)}
                        <div class="subtle-text" style="margin-top: 4px;">{keyMemoryMeta(keyMemory)}</div>
                      {/if}
                    </div>
                    {#if keyMemory.review_status && keyMemory.review_status !== 'approved'}
                      <StatusBadge tone={reviewTone(keyMemory.review_status)} label={keyMemory.review_status} />
                    {/if}
                  </div>
                  <div class="message-body key-memory-value">{keyMemorySummary(keyMemory.value)}</div>
                  {#if keyMemoryFacts(keyMemory).length > 0}
                    <div class="key-memory-facts">
                      {#each keyMemoryFacts(keyMemory) as fact}
                        <div class="key-memory-fact">
                          <span class="sidebar-label">{fact.label}</span>
                          <span class="subtle-text">{fact.value}</span>
                        </div>
                      {/each}
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </section>
      </div>
    </div>
  {/if}
</section>

<ConfirmActionDialog
  open={Boolean(revokeCandidateId)}
  title="Revoke API key?"
  description="Clients using this key will lose access immediately. This cannot be undone."
  subject={revokeCandidateId}
  confirmLabel="Revoke key"
  on:cancel={() => (revokeCandidateId = '')}
  on:confirm={async () => {
    const keyId = revokeCandidateId;
    if (!keyId) return;
    await revokeKey(keyId);
    revokeCandidateId = '';
  }}
/>

<ConfirmActionDialog
  bind:open={rebuildConfirmOpen}
  title="Rebuild retained memory?"
  description="The worker will replay every retained source and activate the resulting generation when it completes."
  subject={$page.params.id}
  confirmLabel="Start rebuild"
  tone="primary"
  busy={rebuilding}
  on:confirm={async () => {
    await rebuildAllRetainedMemory();
    rebuildConfirmOpen = false;
  }}
/>

<style>
  .user-detail-columns .card { display: grid; align-content: start; gap: 18px; }
  .profile-strip > div { min-width: 0; overflow-wrap: anywhere; }

  .user-detail-columns {
    grid-template-columns: minmax(0, 1.03fr) minmax(360px, 0.97fr);
  }

  .compact-kpis .kpi-card {
    gap: 6px;
  }

  .profile-strip {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
    padding: 14px 16px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
  }

  .key-memory-stack {
    gap: 14px;
  }

  .generation-list,
  .session-list {
    display: grid;
    gap: 12px;
  }

  .generation-card {
    background: var(--surface);
  }

  .session-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 14px 16px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: var(--surface);
    transition: 120ms ease;
  }

  .session-row:hover {
    border-color: var(--border-strong);
    background: var(--surface-hover);
  }

  .session-row-main,
  .session-row-side {
    display: flex;
  }

  .session-row-main {
    min-width: 0;
    flex: 1;
    flex-direction: column;
    gap: 4px;
  }

  .session-row-side {
    flex-wrap: wrap;
    justify-content: flex-end;
    align-items: center;
    gap: 8px;
  }

  .session-provider, .session-tokens { color: var(--text-muted); font-size: var(--font-size-sm); white-space: nowrap; }

  .key-memory-card {
    background: var(--surface);
  }

  .key-memory-value {
    padding: 14px 16px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
    color: var(--text);
  }

  .key-memory-facts {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    margin-top: 12px;
  }

  .key-memory-fact {
    display: grid;
    gap: 4px;
    padding: 4px 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  @media (max-width: 900px) {
    .user-detail-columns,
    .profile-strip,
    .key-memory-facts,
    .session-row {
      grid-template-columns: 1fr;
    }

    .session-row {
      flex-direction: column;
      align-items: flex-start;
    }

    .session-row-side {
      justify-content: flex-start;
    }

    .profile-strip,
    .key-memory-facts {
      grid-template-columns: 1fr;
    }
  }
</style>
