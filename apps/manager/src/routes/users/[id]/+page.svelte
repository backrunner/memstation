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
  import JsonView from '$lib/components/JsonView.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { MemoryGeneration, UserDetailPayload, UserSourcesPayload } from '$lib/types';

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
  let embeddingBaseUrl = '';
  let embeddingApiKey = '';
  let embeddingApiKeyConfigured = false;
  let useCustomEmbeddingEndpoint = false;
  let useCustomEmbeddingApiKey = false;
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
    embeddingBaseUrl = String(detail?.settings?.embedding_base_url ?? '');
    embeddingApiKey = '';
    embeddingApiKeyConfigured = Boolean(detail?.settings?.embedding_api_key_configured);
    useCustomEmbeddingEndpoint = embeddingBaseUrl.trim().length > 0;
    useCustomEmbeddingApiKey = embeddingApiKeyConfigured;
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
        embedding_base_url: useCustomEmbeddingEndpoint ? embeddingBaseUrl.trim() : '',
        clear_embedding_api_key: !useCustomEmbeddingApiKey,
        retain_source_materials: retainSourceMaterials,
        tool_write_enabled: toolWriteEnabled,
        max_injection_tokens: maxInjectionTokens
      };
      if (useCustomEmbeddingApiKey && embeddingApiKey.trim().length > 0) {
        payload.embedding_api_key = embeddingApiKey.trim();
      }
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
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">User Detail</span>
    <h1 class="page-title">{$page.params.id}</h1>
    <p class="page-description">
      Review settings, key inventory, recent conversations, key memories, and token usage for this user without proxying through the manager backend.
    </p>
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
        <div class="page-description" style="margin-top: 8px;">Copy this secret now. The target will only reveal it once.</div>
        <pre class="json-block" style="margin-top: 12px;">{createdKey}</pre>
      </div>
    {/if}

    <div class="kpi-grid">
      <div class="kpi-card">
        <span class="kpi-label">API Keys</span>
        <strong class="kpi-value">{detail.stats.api_key_count}</strong>
        <span class="kpi-meta">Active and revoked keys for this user</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Conversations</span>
        <strong class="kpi-value">{detail.stats.conversation_count}</strong>
        <span class="kpi-meta">Persisted sessions on the selected target</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Key Memories</span>
        <strong class="kpi-value">{detail.stats.key_memory_count}</strong>
        <span class="kpi-meta">Operator-visible memory slots</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Total Tokens</span>
        <strong class="kpi-value">{detail.token_usage.total_tokens.toLocaleString()}</strong>
        <span class="kpi-meta">Usage for this user</span>
      </div>
      <div class="kpi-card">
        <span class="kpi-label">Retained Snapshots</span>
        <strong class="kpi-value">{sources?.source_stats.snapshot_count ?? 0}</strong>
        <span class="kpi-meta">Normalized source materials available for rebuild</span>
      </div>
    </div>

    <div class="grid-2" style="align-items: start;">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Profile</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Identity and runtime settings</h2>
          </div>
          {#if detail.user.is_admin}
            <StatusBadge tone="brand" label="Admin" />
          {/if}
        </div>

        <div class="toolbar">
          <div class="debug-block">
            <div class="sidebar-label">Name</div>
            <div>{detail.user.name}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">User ID</div>
            <div>{detail.user.id}</div>
          </div>
          <div class="debug-block">
            <div class="sidebar-label">Created</div>
            <div>{new Date(detail.user.created_at).toLocaleString()}</div>
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
          <div class="field" style="grid-column: 1 / -1;">
            <span class="field-label">Embedding endpoint</span>
            <div class="inline-group" style="margin-bottom: 10px;">
              <button class={useCustomEmbeddingEndpoint ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (useCustomEmbeddingEndpoint = true)}>Custom endpoint</button>
              <button class={!useCustomEmbeddingEndpoint ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (useCustomEmbeddingEndpoint = false)}>Use provider default</button>
            </div>
            {#if useCustomEmbeddingEndpoint}
              <input class="input" bind:value={embeddingBaseUrl} placeholder="https://api.example.com/v1" />
              <div class="field-hint">Must expose an OpenAI-compatible `POST /embeddings` endpoint.</div>
            {:else}
              <div class="field-hint">This user follows the global OpenAI provider endpoint configured in system settings.</div>
            {/if}
          </div>
          <div class="field" style="grid-column: 1 / -1;">
            <span class="field-label">Embedding API key</span>
            <div class="inline-group" style="margin-bottom: 10px;">
              <button class={useCustomEmbeddingApiKey ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (useCustomEmbeddingApiKey = true)}>Custom key</button>
              <button class={!useCustomEmbeddingApiKey ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (useCustomEmbeddingApiKey = false)}>Use provider default</button>
            </div>
            {#if useCustomEmbeddingApiKey}
              <input class="input" bind:value={embeddingApiKey} type="password" autocomplete="off" placeholder={embeddingApiKeyConfigured ? 'Leave blank to keep the current secret' : 'sk-...'} />
              <div class="field-hint">{embeddingApiKeyConfigured ? 'A custom secret is already stored. Leave this blank to keep it, or enter a new one to rotate it.' : 'Stored encrypted on the target. Leave empty until you want to set a custom secret.'}</div>
            {:else}
              <div class="field-hint">This user will inherit the global OpenAI provider credential for embeddings.</div>
            {/if}
          </div>
          <div class="field">
            <span class="field-label">Tool writes enabled</span>
            <div class="inline-group">
              <button class={toolWriteEnabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (toolWriteEnabled = true)}>Enabled</button>
              <button class={!toolWriteEnabled ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (toolWriteEnabled = false)}>Disabled</button>
            </div>
          </div>
          <div class="field">
            <span class="field-label">Retain source materials</span>
            <div class="inline-group">
              <button class={retainSourceMaterials ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (retainSourceMaterials = true)}>Enabled</button>
              <button class={!retainSourceMaterials ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (retainSourceMaterials = false)}>Disabled</button>
            </div>
            <div class="field-hint">Store normalized source snapshots so future structure or embedding rebuilds can replay this user's conversations.</div>
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
            <h2 class="page-title" style="font-size: 1.35rem;">Source snapshot and generation status</h2>
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
                    <div class="subtle-text">{generation.generation_id}</div>
                  </div>
                  <StatusBadge tone={generationTone(generation)} label={generation.is_active ? 'active' : generation.status} />
                </div>
                <div class="helper-text">{generation.processed_count}/{generation.source_count} snapshots · {generation.embedding_count} embeddings</div>
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
            <h2 class="page-title" style="font-size: 1.35rem;">Create, inspect, and revoke credentials</h2>
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
            <h2 class="page-title" style="font-size: 1.35rem;">Most recent sessions</h2>
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
          <div class="toolbar">
            {#each detail.key_memories as keyMemory}
              <div class="debug-block">
                <div class="inline-group" style="justify-content: space-between; margin-bottom: 10px;">
                  <strong>{keyMemory.key}</strong>
                  <StatusBadge tone="muted" label={keyMemory.category ?? 'uncategorized'} />
                </div>
                <JsonView value={keyMemory} />
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</section>
