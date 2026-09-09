<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchUsers } from '$lib/api/client';
  import FilterToolbar from '$lib/components/FilterToolbar.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import SelectField from '$lib/components/SelectField.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { UserSummary } from '$lib/types';

  let loading = true;
  let error = '';
  let search = '';
  let sortBy = 'recent';
  let users: UserSummary[] = [];
  let lastLoadKey = '';
  let mounted = false;
  let loadRevision = 0;

  onMount(() => {
    mounted = true;
    return () => { loadRevision++; };
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (mounted && loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadUsers();
  }

  async function loadUsers(): Promise<void> {
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
      const nextUsers = await fetchUsers(target);
      if (revision === loadRevision && scope === loadKey) users = nextUsers;
    } catch (cause) {
      if (revision === loadRevision && scope === loadKey) error = cause instanceof Error ? cause.message : 'Failed to load users';
    } finally {
      if (revision === loadRevision && scope === loadKey) loading = false;
    }
  }

  const sortItems = [
    { value: 'recent', label: 'Most recently active' },
    { value: 'tokens', label: 'Highest token usage' },
    { value: 'memory', label: 'Most key memories' },
    { value: 'conversations', label: 'Most conversations' },
    { value: 'name', label: 'Name' }
  ];

  $: filtered = users.filter((item) => {
    const query = search.trim().toLowerCase();
    if (!query) {
      return true;
    }
    return [item.user_id, item.name].join(' ').toLowerCase().includes(query);
  }).sort((left, right) => {
    if (sortBy === 'tokens') return right.total_tokens - left.total_tokens;
    if (sortBy === 'memory') return right.key_memory_count - left.key_memory_count;
    if (sortBy === 'conversations') return right.conversation_count - left.conversation_count;
    if (sortBy === 'name') return left.name.localeCompare(right.name);
    return new Date(right.recent_active_at ?? 0).getTime() - new Date(left.recent_active_at ?? 0).getTime();
  });
</script>

<section class="page-stack">
  <PageHeader
    eyebrow="Inspect"
    title="Users"
    description="Find active users, identify accounts carrying the most memory or token load, and jump into per-user operational detail."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect users.</div>
  {:else}
    <section class="filter-bar">
      <FilterToolbar activeCount={sortBy === 'recent' ? 0 : 1} summary={`${filtered.length} of ${users.length} users`}>
        <TextInputField label="Search" bind:value={search} placeholder="Search users" />
        {#snippet actions()}
          <button class="btn-secondary" type="button" disabled={loading} on:click={loadUsers}>Refresh</button>
        {/snippet}
        {#snippet filters()}
          <SelectField label="Sort" bind:value={sortBy} items={sortItems} />
        {/snippet}
      </FilterToolbar>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {:else if loading}
      <div class="notice-card">Loading users…</div>
    {:else}
      <section class="table-card">
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>User</th>
                <th>API Keys</th>
                <th>Conversations</th>
                <th>Key Memories</th>
                <th>Total Tokens</th>
                <th>Last Active</th>
              </tr>
            </thead>
            <tbody>
              {#each filtered as item}
                <tr>
                  <td>
                    <a href={`/users/${item.user_id}`}>
                      <strong>{item.name}</strong>
                    </a>
                    <div class="subtle-text mono">{item.user_id}</div>
                    {#if item.is_admin}
                      <div style="margin-top: 4px;">
                        <StatusBadge tone="brand" label="Admin" />
                      </div>
                    {/if}
                  </td>
                  <td class="mono">{item.api_key_count}</td>
                  <td class="mono">{item.conversation_count}</td>
                  <td class="mono">{item.key_memory_count}</td>
                  <td class="mono">{item.total_tokens.toLocaleString()}</td>
                  <td class="subtle-text no-wrap">
                    {item.recent_active_at ? new Date(item.recent_active_at).toLocaleString() : 'No activity yet'}
                  </td>
                </tr>
              {/each}
              {#if filtered.length === 0}
                <tr>
                  <td colspan="6">
                    <div class="empty-state" style="border: none; padding: 12px 0;">
                      {search ? 'No users match the current filter.' : 'No users on this target yet.'}
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
