<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  import {
    createMemoryRebuild,
    createUserAPIKey,
    fetchUserDetail,
    fetchUserSources,
    revokeUserAPIKey,
    updateUserSettings
  } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { KeyMemoryRecord, MemoryGeneration, UserDetailPayload, UserSourcesPayload } from '$lib/types';

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

  onMount(async () => {
    await loadDetail();
  });

  $: routeKey = `${$selectedTarget?.id ?? 'none'}:${$page.params.id}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (routeKey !== lastTargetKey && !$targetState.session.checking) {
    lastTargetKey = routeKey;
    void loadDetail();
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

  async function loadDetail(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated || !$page.params.id) {
      loading = false;
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
      await createMemoryRebuild($selectedTarget, {
        userId: $page.params.id,
        scope: 'user',
        activateOnSuccess: true
      });
      sources = await fetchUserSources($selectedTarget, $page.params.id);
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
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">User Detail</span>
    <h1 class="page-title">{$page.params.id}</h1>
    <p class="page-description">Settings, keys, recent sessions, and memory for this user.</p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect user detail pages.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading user detail...</div>
  {:else if detail}
    {#if success}
      <div class="notice-card">{success}</div>
    {/if}
    {#if createdKey}
      <div class="card">
        <div class="sidebar-label">New API key</div>
        <div class="page-description" style="margin-top: 8px;">Shown once.</div>
        <pre class="json-block" style="margin-top: 12px;">{createdKey}</pre>
      </div>
    {/if}

    <div class="kpi-grid compact-kpis">
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

    <div class="grid-2" style="align-items: start;">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Profile</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Identity and settings</h2>
          </div>
          {#if detail.user.is_admin}
            <StatusBadge tone="brand" label="Admin" />
          {/if}
        </div>

        <div class="profile-strip">
          <div>
            <div class="sidebar-label">Name</div>
            <strong>{detail.user.name}</strong>
          </div>
          <div>
            <div class="sidebar-label">User ID</div>
            <span>{detail.user.id}</span>
          </div>
          <div>
            <div class="sidebar-label">Created</div>
            <span>{new Date(detail.user.created_at).toLocaleString()}</span>
          </div>
        </div>

        <div class="form-grid two-up" style="margin-top: 12px;">
          <label class="field">
            <span class="field-label">Chat model</span>
            <input class="input" bind:value={chatModel} />
          </label>
          <label class="field">
            <span class="field-label">Summary model</span>
            <input class="input" bind:value={summaryModel} />
          </label>
          <label class="field">
            <span class="field-label">Analysis model</span>
            <input class="input" bind:value={analysisModel} />
          </label>
          <label class="field">
            <span class="field-label">Embedding model</span>
            <input class="input" bind:value={embeddingModel} />
          </label>
          <div class="field">
            <span class="field-label">Tool writes</span>
            <div class="inline-group">
              <button class={toolWriteEnabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (toolWriteEnabled = true)}>On</button>
              <button class={!toolWriteEnabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (toolWriteEnabled = false)}>Off</button>
            </div>
          </div>
          <div class="field">
            <span class="field-label">Source retention</span>
            <div class="inline-group">
              <button class={retainSourceMaterials ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (retainSourceMaterials = true)}>On</button>
              <button class={!retainSourceMaterials ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (retainSourceMaterials = false)}>Off</button>
            </div>
          </div>
          <label class="field">
            <span class="field-label">Max injection tokens</span>
            <input class="input" type="number" bind:value={maxInjectionTokens} />
          </label>
        </div>

        <div class="inline-group" style="justify-content: flex-end; margin-top: 16px;">
          <button class="button" type="button" disabled={saving} on:click={saveSettings}>{saving ? 'Saving...' : 'Save user settings'}</button>
        </div>
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Retention & rebuild</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Retention and rebuild</h2>
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
        {#if rebuildRequired}
          <div class="notice-card" style="margin-top: 12px;">Current settings differ from the active memory generation. Trigger a rebuild to replay retained sources with the new configuration.</div>
        {/if}
        <div class="inline-group" style="justify-content: flex-end; margin-top: 12px;">
          <button class="button" type="button" disabled={rebuilding || (sources?.source_stats.snapshot_count ?? 0) === 0} on:click={rebuildAllRetainedMemory}>
            {rebuilding ? 'Creating rebuild...' : 'Rebuild all retained memory'}
          </button>
        </div>
        <div class="toolbar" style="margin-top: 12px;">
          {#if (sources?.generations.length ?? 0) === 0}
            <div class="empty-state">No memory generations are available for this user yet.</div>
          {:else}
            {#each sources?.generations ?? [] as generation}
              <div class="debug-block">
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
    </div>

    <div class="grid-2" style="align-items: start;">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">API keys</div>
            <h2 class="page-title" style="font-size: 1.35rem;">API keys</h2>
          </div>
        </div>
        <div class="inline-group" style="align-items: end; margin-bottom: 14px;">
          <label class="field" style="flex: 1;">
            <span class="field-label">New key label</span>
            <input class="input" bind:value={newKeyName} placeholder="build-bot or local playground" />
          </label>
          <button class="button" type="button" disabled={creatingKey} on:click={createKey}>{creatingKey ? 'Creating...' : 'Create API key'}</button>
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
                  <div style="margin-top: 12px;">
                    <button class="button-danger" type="button" on:click={() => revokeKey(apiKey.id)}>Revoke key</button>
                  </div>
                {/if}
              </div>
            {/each}
          {/if}
        </div>
      </section>

      <section class="table-card">
        <div class="card" style="border: none; box-shadow: none; border-radius: 0;">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Recent conversations</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Recent sessions</h2>
          </div>
          <a class="button-ghost" href={`/memory?user_id=${detail.user.id}`}>Open memory workspace</a>
        </div>
        </div>
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>Conversation</th>
                <th>Provider</th>
                <th>Status</th>
                <th>Tokens</th>
              </tr>
            </thead>
            <tbody>
              {#each detail.conversations as conversation}
                <tr>
                  <td>
                    <a href={`/conversations/${conversation.conversation_id}?user_id=${detail.user.id}`}>
                      <strong>{conversation.conversation_id}</strong>
                    </a>
                    <div class="subtle-text">{conversation.protocol} · {conversation.mode}</div>
                  </td>
                  <td>{conversation.provider}</td>
                  <td>
                    <StatusBadge tone={conversation.finalize_status === 'ended' ? 'success' : conversation.finalize_status === 'failed' ? 'danger' : 'warning'} label={conversation.finalize_status} />
                  </td>
                  <td>{conversation.total_tokens}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Recent key memories</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Stable memory slots</h2>
          </div>
          <a class="button-ghost" href={`/memory/keys?user_id=${detail.user.id}`}>Manage key memories</a>
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
                  <div class="inline-group" style="justify-content: flex-end;">
                    {#if keyMemory.review_status && keyMemory.review_status !== 'approved'}
                      <StatusBadge tone={reviewTone(keyMemory.review_status)} label={keyMemory.review_status} />
                    {/if}
                    <a class="button-ghost" href={`/memory/keys?user_id=${detail.user.id}`}>Open workspace</a>
                  </div>
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
  {/if}
</section>

<style>
  .compact-kpis .kpi-card {
    gap: 6px;
  }

  .profile-strip {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
    padding: 14px 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
  }

  .key-memory-stack {
    gap: 14px;
  }

  .key-memory-card {
    background: rgba(255, 255, 255, 0.94);
  }

  .key-memory-value {
    padding: 14px 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
    color: var(--text-primary);
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
    padding: 10px 12px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-sm);
    background: rgba(255, 255, 255, 0.72);
  }

  @media (max-width: 900px) {
    .profile-strip,
    .key-memory-facts {
      grid-template-columns: 1fr;
    }
  }
</style>
