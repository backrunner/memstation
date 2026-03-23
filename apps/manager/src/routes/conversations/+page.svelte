<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConversations } from '$lib/api/client';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { ConversationListItem } from '$lib/types';

  let loading = true;
  let error = '';
  let lastLoadKey = '';
  let search = '';
  let conversations: ConversationListItem[] = [];

  onMount(async () => {
    await loadConversations();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadConversations();
  }

  async function loadConversations(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      conversations = await fetchConversations($selectedTarget);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load conversations';
    } finally {
      loading = false;
    }
  }

  $: filtered = conversations.filter((item) => {
    const query = search.trim().toLowerCase();
    if (!query) {
      return true;
    }
    return [item.conversation_id, item.provider, item.protocol, item.mode, item.finalize_status]
      .join(' ')
      .toLowerCase()
      .includes(query);
  });
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Conversations</span>
    <h1 class="page-title">Trace each request chain from protocol ingress to memory extraction</h1>
    <p class="page-description">
      Browse persisted conversations, inspect finalize and worker states, and open a single session to debug tool round-trips or post-conversation extraction outcomes.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect conversation history.</div>
  {:else}
    <section class="card">
      <div class="filters two-up">
        <label class="field">
          <span class="field-label">Search</span>
          <input class="input" bind:value={search} placeholder="conversation id, provider, protocol, mode..." />
        </label>
        <div class="field" style="align-self: end;">
          <button class="button-secondary" type="button" on:click={loadConversations}>Refresh list</button>
        </div>
      </div>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {:else if loading}
      <div class="notice-card">Loading conversations...</div>
    {:else}
      <section class="table-card">
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>Conversation</th>
                <th>Provider</th>
                <th>Protocol</th>
                <th>Status</th>
                <th>Messages</th>
                <th>Tokens</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {#each filtered as item}
                <tr>
                  <td>
                    <a href={`/conversations/${item.conversation_id}`}>
                      <strong>{item.conversation_id}</strong>
                    </a>
                    <div class="subtle-text">{item.mode}</div>
                  </td>
                  <td>{item.provider}</td>
                  <td>{item.protocol}</td>
                  <td>
                    <div class="toolbar">
                      <StatusBadge tone={item.finalize_status === 'ended' ? 'success' : item.finalize_status === 'active' ? 'brand' : item.finalize_status === 'failed' ? 'danger' : 'warning'} label={item.finalize_status} />
                      <StatusBadge tone={item.summary_status === 'completed' ? 'success' : item.summary_status === 'failed' ? 'danger' : item.summary_status === 'skipped' ? 'muted' : 'warning'} label={`summary ${item.summary_status}`} />
                    </div>
                  </td>
                  <td>{item.message_count}</td>
                  <td>{item.total_tokens}</td>
                  <td>{new Date(item.updated_at).toLocaleString()}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}
  {/if}
</section>
