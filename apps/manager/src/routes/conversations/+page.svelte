<svelte:options runes={false} />

<script lang="ts">
  import { workspaceEntryURL } from '#lib/scope/navigation.ts';
  import Button from '#lib/components/Button.svelte';
  import { onMount } from 'svelte';
  import { RefreshCw } from '@lucide/svelte';

  import { fetchConversations, fetchUsers } from '#lib/api/client.ts';
  import FilterToolbar from '#lib/components/FilterToolbar.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { ConversationListItem } from '#lib/types.ts';

  let loading = true;
  let error = '';
  let lastLoadKey = '';
  let mounted = false;
  let loadRevision = 0;
  let search = '';
  let providerFilter = '';
  let modeFilter = '';
  let statusFilter = '';
  let dateFrom = '';
  let dateTo = '';
  let conversations: ConversationListItem[] = [];

  onMount(() => {
    mounted = true;
    return () => { loadRevision++; };
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (mounted && loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadConversations();
  }

  async function loadConversations(): Promise<void> {
    const revision = ++loadRevision;
    const scope = loadKey;
    const target = $selectedTarget;
    if (!target || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      const users = await fetchUsers(target);
      if (revision !== loadRevision || scope !== loadKey) return;
      const lists = await Promise.all(
        (users.length > 0 ? users : [{ user_id: 'admin' }]).map((user) =>
          fetchConversations(target, { userId: user.user_id, limit: 100 })
        )
      );
      if (revision !== loadRevision || scope !== loadKey) return;
      conversations = lists.flat().sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime());
    } catch (cause) {
      if (revision === loadRevision && scope === loadKey) error = cause instanceof Error ? cause.message : 'Failed to load conversations';
    } finally {
      if (revision === loadRevision && scope === loadKey) loading = false;
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
      <FilterToolbar activeCount={[providerFilter, modeFilter, statusFilter, dateFrom, dateTo].filter(Boolean).length} summary={`${filtered.length} of ${conversations.length} conversations`}>
        <TextInputField label="Search" bind:value={search} placeholder="Search conversations" />
        {#snippet actions()}
          <Button class="btn-secondary" type="button" disabled={loading} onclick={loadConversations}>
            <RefreshCw size={14} aria-hidden="true" /> {loading ? 'Refreshing…' : 'Refresh'}
          </Button>
        {/snippet}
        {#snippet filters()}
          <div class="filters three-up">
            <SelectField label="Provider" bind:value={providerFilter} items={providerItems} />
            <SelectField label="Mode" bind:value={modeFilter} items={modeItems} />
            <SelectField label="Finalize status" bind:value={statusFilter} items={statusItems} />
            <TextInputField label="Date from" bind:value={dateFrom} type="date" />
            <TextInputField label="Date to" bind:value={dateTo} type="date" />
            <div class="field" style="align-self: end;">
              <Button class="btn-ghost" type="button" onclick={clearFilters}>Clear filters</Button>
            </div>
          </div>
        {/snippet}
      </FilterToolbar>
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
                    <a href={workspaceEntryURL($selectedTarget?.id, `/conversations/${encodeURIComponent(item.conversation_id)}?user_id=${encodeURIComponent(item.user_id)}`)}>
                      <strong class="mono">{item.conversation_id}</strong>
                    </a>
                    <div class="subtle-text">{item.mode}</div>
                  </td>
                  <td>{item.provider}</td>
                  <td class="mono">{item.protocol}</td>
                  <td>
                    <div>
                      <StatusBadge
                        tone={finalizeStatusTone(item.finalize_status)}
                        label={item.finalize_status}
                      />
                    </div>
                    <div class="conversation-processing">
                      {#if !['completed', 'skipped'].includes(item.summary_status)}
                      <StatusBadge
                        tone={summaryStatusTone(item.summary_status)}
                        label={`summary ${item.summary_status}`}
                      />
                      {/if}
                      {#if !['completed', 'skipped'].includes(item.analysis_status)}
                      <StatusBadge
                        tone={summaryStatusTone(item.analysis_status)}
                        label={`analysis ${item.analysis_status}`}
                      />
                      {/if}
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
                      {search || providerFilter || modeFilter || statusFilter || dateFrom || dateTo ? 'No conversations match the current filter.' : 'No conversations recorded on this target yet.'}
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
