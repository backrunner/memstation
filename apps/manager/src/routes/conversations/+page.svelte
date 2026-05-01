<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchConversations } from '$lib/api/client';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
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

  function finalizeStatusTone(status: string): 'brand' | 'success' | 'warning' | 'danger' | 'muted' {
    if (status === 'ended') return 'success';
    if (status === 'active') return 'brand';
    if (status === 'failed') return 'danger';
    return 'warning';
  }

  function summaryStatusTone(status: string): 'success' | 'danger' | 'muted' | 'warning' {
    if (status === 'completed') return 'success';
    if (status === 'failed') return 'danger';
    if (status === 'skipped') return 'muted';
    return 'warning';
  }
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Inspect"
    title="Conversations"
    description="Browse persisted conversations, inspect finalize and worker states, and open a session to debug tool round-trips or post-conversation extraction outcomes."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect conversation history.</div>
  {:else}
    <section class="card">
      <div class="filters two-up">
        <TextInputField
          label="Search"
          bind:value={search}
          placeholder="conversation id, provider, protocol, mode…"
          fieldClass="field-span-2"
        />
        <div class="field" style="align-self: end;">
          <div class="inline-group" style="justify-content: space-between;">
            <span class="helper-text">{filtered.length} of {conversations.length} conversations</span>
            <button class="btn-secondary btn-sm" type="button" on:click={loadConversations}>Refresh</button>
          </div>
        </div>
      </div>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {:else if loading}
      <div class="notice-card">Loading conversations…</div>
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
                      <strong class="mono">{item.conversation_id}</strong>
                    </a>
                    <div class="subtle-text">{item.mode}</div>
                  </td>
                  <td>{item.provider}</td>
                  <td class="mono">{item.protocol}</td>
                  <td>
                    <div class="inline-group" style="gap: 4px;">
                      <StatusBadge
                        tone={finalizeStatusTone(item.finalize_status)}
                        label={item.finalize_status}
                      />
                      <StatusBadge
                        tone={summaryStatusTone(item.summary_status)}
                        label={`summary ${item.summary_status}`}
                      />
                    </div>
                  </td>
                  <td class="mono">{item.message_count}</td>
                  <td class="mono">{item.total_tokens}</td>
                  <td class="subtle-text no-wrap">{new Date(item.updated_at).toLocaleString()}</td>
                </tr>
              {/each}
              {#if filtered.length === 0}
                <tr>
                  <td colspan="7">
                    <div class="empty-state" style="border: none; padding: 12px 0;">
                      {search ? 'No conversations match the current filter.' : 'No conversations recorded on this target yet.'}
                    </div>
                  </td>
                </tr>
              {/if}
            </tbody>
          </table>
        </div>
      </section>
    {/if}
  {/if}
</section>
