<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchUsers } from '$lib/api/client';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { UserSummary } from '$lib/types';

  let loading = true;
  let error = '';
  let search = '';
  let users: UserSummary[] = [];
  let lastLoadKey = '';

  onMount(async () => {
    await loadUsers();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadUsers();
  }

  async function loadUsers(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      users = await fetchUsers($selectedTarget);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load users';
    } finally {
      loading = false;
    }
  }

  $: filtered = users.filter((item) => {
    const query = search.trim().toLowerCase();
    if (!query) {
      return true;
    }
    return [item.user_id, item.name].join(' ').toLowerCase().includes(query);
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
    <section class="card">
      <div class="filters two-up">
        <TextInputField
          label="Search"
          bind:value={search}
          placeholder="user id or display name"
          fieldClass="field-span-2"
        />
        <div class="field" style="align-self: end;">
          <div class="inline-group" style="justify-content: space-between;">
            <span class="helper-text">{filtered.length} of {users.length} users</span>
            <button class="btn-secondary btn-sm" type="button" on:click={loadUsers}>Refresh</button>
          </div>
        </div>
      </div>
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
