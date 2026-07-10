<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { RefreshCw } from 'lucide-svelte';

  import { fetchConversations, fetchUsers } from '$lib/api/client';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import SelectField from '$lib/components/SelectField.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { ConversationListItem } from '$lib/types';

  let loading = true;
  let error = '';
  let lastLoadKey = '';
  let search = '';
  let providerFilter = '';
  let modeFilter = '';
  let statusFilter = '';
  let dateFrom = '';
  let dateTo = '';
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
      const users = await fetchUsers($selectedTarget);
      const lists = await Promise.all(
        (users.length > 0 ? users : [{ user_id: 'admin' }]).map((user) =>
          fetchConversations($selectedTarget, { userId: user.user_id, limit: 100 })
        )
      );
      conversations = lists.flat().sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime());
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load conversations';
    } finally {
      loading = false;
    }
  }

  $: filtered = conversations.filter((item) => {
    const query = search.trim().toLowerCase();
    const matchesQuery = !query || [item.conversation_id, item.user_id, item.provider, item.protocol, item.mode, item.finalize_status]
      .join(' ')
      .toLowerCase()
      .includes(query);
    const updatedAt = new Date(item.updated_at).getTime();
    const from = dateFrom ? new Date(`${dateFrom}T00:00:00`).getTime() : Number.NEGATIVE_INFINITY;
    const to = dateTo ? new Date(`${dateTo}T23:59:59.999`).getTime() : Number.POSITIVE_INFINITY;
    return matchesQuery
      && (!providerFilter || item.provider === providerFilter)
      && (!modeFilter || item.mode === modeFilter)
      && (!statusFilter || item.finalize_status === statusFilter)
      && updatedAt >= from
      && updatedAt <= to;
  });

  $: providerItems = filterItems(conversations.map((item) => item.provider), 'All providers');
  $: modeItems = filterItems(conversations.map((item) => item.mode), 'All modes');
  $: statusItems = filterItems(conversations.map((item) => item.finalize_status), 'All statuses');

  function filterItems(values: string[], allLabel: string): Array<{ value: string; label: string }> {
    return [
      { value: '', label: allLabel },
      ...Array.from(new Set(values.filter(Boolean))).sort().map((value) => ({ value, label: value }))
    ];
  }

  function clearFilters(): void {
    search = '';
    providerFilter = '';
    modeFilter = '';
    statusFilter = '';
    dateFrom = '';
    dateTo = '';
  }

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
    title="Conversations"
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect conversation history.</div>
  {:else}
    <section class="filter-bar">
      <div class="filters three-up">
        <TextInputField
          label="Search"
          bind:value={search}
          placeholder="conversation id, provider, protocol, mode…"
          fieldClass="field-span-3"
        />
        <SelectField label="Provider" bind:value={providerFilter} items={providerItems} />
        <SelectField label="Mode" bind:value={modeFilter} items={modeItems} />
        <SelectField label="Finalize status" bind:value={statusFilter} items={statusItems} />
        <TextInputField label="Date from" bind:value={dateFrom} type="date" />
        <TextInputField label="Date to" bind:value={dateTo} type="date" />
        <div class="field" style="align-self: end;">
          <div class="inline-group" style="justify-content: space-between;">
            <span class="helper-text">{filtered.length} of {conversations.length} conversations</span>
            <div class="inline-group">
              <button class="btn-ghost btn-sm" type="button" on:click={clearFilters}>Clear</button>
              <button
                class="btn-secondary btn-icon btn-sm"
                type="button"
                on:click={loadConversations}
                aria-label="Refresh conversations"
                title="Refresh conversations"
              ><RefreshCw size={14} strokeWidth={1.8} /></button>
            </div>
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
          <table class="data-table conversation-table">
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
                      <StatusBadge
                        tone={summaryStatusTone(item.analysis_status)}
                        label={`analysis ${item.analysis_status}`}
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
