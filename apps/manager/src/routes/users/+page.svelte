<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchUsers } from '$lib/api/client';
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
  <div class="hero-card">
    <span class="sidebar-label">Users</span>
    <h1 class="page-title">Users</h1>
    <p class="page-description">
      Use this view to find active users quickly, identify which accounts carry the most memory or token load, and jump into per-user operational detail.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect users on the selected target.</div>
  {:else}
    <section class="card">
      <div class="page-header">
        <TextInputField
          label="Search"
          bind:value={search}
          placeholder="user id or display name"
          fieldClass="field-span-2"
        />
        <button class="button-secondary" type="button" on:click={loadUsers}>Refresh Users</button>
      </div>
    </section>

    {#if error}
      <div class="error-state">{error}</div>
    {:else if loading}
      <div class="notice-card">Loading users...</div>
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
                <th>Recent Activity</th>
              </tr>
            </thead>
            <tbody>
              {#each filtered as item}
                <tr>
                  <td>
                    <a href={`/users/${item.user_id}`}>
                      <strong>{item.name}</strong>
                    </a>
                    <div class="subtle-text">{item.user_id}</div>
                    {#if item.is_admin}
                      <div style="margin-top: 8px;">
                        <StatusBadge tone="brand" label="Admin" />
                      </div>
                    {/if}
                  </td>
                  <td>{item.api_key_count}</td>
                  <td>{item.conversation_count}</td>
                  <td>{item.key_memory_count}</td>
                  <td>{item.total_tokens.toLocaleString()}</td>
                  <td>{item.recent_active_at ? new Date(item.recent_active_at).toLocaleString() : 'No activity yet'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}
  {/if}
</section>
