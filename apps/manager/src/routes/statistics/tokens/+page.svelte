<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { Select } from 'bits-ui';

  import { fetchConversations, fetchUsers } from '$lib/api/client';
  import EChart from '$lib/components/EChart.svelte';
  import KpiCard from '$lib/components/KpiCard.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { ConversationListItem, UserSummary } from '$lib/types';

  const rangeItems = [
    { value: '7', label: 'Last 7 days' },
    { value: '30', label: 'Last 30 days' },
    { value: '90', label: 'Last 90 days' }
  ];

  let loading = true;
  let error = '';
  let lastLoadKey = '';
  let users: UserSummary[] = [];
  let conversationsByUser: Record<string, ConversationListItem[]> = {};
  let range = '30';
  let selectedUserId = 'all';
  let selectedProvider = 'all';

  onMount(async () => {
    await loadData();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadData();
  }

  async function loadData(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      users = await fetchUsers($selectedTarget);
      const entries = await Promise.all(
        users.map(async (user) => ({
          userId: user.user_id,
          conversations: await fetchConversations($selectedTarget, { userId: user.user_id, limit: 60 })
        }))
      );
      conversationsByUser = Object.fromEntries(entries.map((entry) => [entry.userId, entry.conversations]));
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load token statistics';
    } finally {
      loading = false;
    }
  }

  $: providerItems = [
    { value: 'all', label: 'All providers' },
    ...Array.from(new Set(users.flatMap((user) => (conversationsByUser[user.user_id] ?? []).map((item) => item.provider)).filter(Boolean)))
      .sort()
      .map((provider) => ({ value: provider, label: provider }))
  ];

  $: userItems = [
    { value: 'all', label: 'All users' },
    ...users.map((user) => ({ value: user.user_id, label: `${user.name} · ${user.user_id}` }))
  ];

  $: allConversations = selectedUserId === 'all'
    ? users.flatMap((user) => conversationsByUser[user.user_id] ?? []).map((item) => ({ ...item, __user_id: item.user_id }))
    : (conversationsByUser[selectedUserId] ?? []).map((item) => ({ ...item, __user_id: item.user_id }));

  $: filteredConversations = allConversations.filter((item) => {
    if (selectedProvider !== 'all' && item.provider !== selectedProvider) {
      return false;
    }
    const ageMs = Date.now() - new Date(item.updated_at).getTime();
    return ageMs <= Number(range) * 24 * 60 * 60 * 1000;
  });

  $: totalTokens = filteredConversations.reduce((sum, item) => sum + item.total_tokens, 0);
  $: averageTokens = filteredConversations.length > 0 ? Math.round(totalTokens / filteredConversations.length) : 0;
  $: activeUsers = new Set(filteredConversations.map((item) => item.user_id)).size;
  $: topConversations = [...filteredConversations].sort((a, b) => b.total_tokens - a.total_tokens).slice(0, 8);

  $: trendBuckets = buildTrend(filteredConversations, Number(range));
  $: trendOption = {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 18, top: 24, bottom: 36 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: trendBuckets.map((item) => item.label),
      axisLine: { lineStyle: { color: '#c8d4df' } },
      axisLabel: { color: '#5a6b7d' }
    },
    yAxis: {
      type: 'value',
      axisLine: { show: false },
      splitLine: { lineStyle: { color: '#e1e9f0' } },
      axisLabel: { color: '#5a6b7d' }
    },
    series: [
      {
        type: 'line',
        smooth: true,
        data: trendBuckets.map((item) => item.total),
        lineStyle: { color: '#1e5aa8', width: 3 },
        areaStyle: { color: 'rgba(30, 90, 168, 0.16)' },
        itemStyle: { color: '#1e5aa8' }
      }
    ]
  };

  $: providerBreakdown = buildBreakdown(filteredConversations);
  $: providerOption = {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 18, top: 24, bottom: 36 },
    xAxis: {
      type: 'category',
      data: providerBreakdown.map((item) => item.label),
      axisLine: { lineStyle: { color: '#c8d4df' } },
      axisLabel: { color: '#5a6b7d' }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#e1e9f0' } },
      axisLabel: { color: '#5a6b7d' }
    },
    series: [
      {
        type: 'bar',
        data: providerBreakdown.map((item) => item.total),
        itemStyle: {
          color: '#2f855a',
          borderRadius: [10, 10, 0, 0]
        }
      }
    ]
  };

  function buildTrend(items: ConversationListItem[], days: number): Array<{ label: string; total: number }> {
    const bucketMap = new Map<string, number>();
    for (let offset = days - 1; offset >= 0; offset -= 1) {
      const date = new Date();
      date.setHours(0, 0, 0, 0);
      date.setDate(date.getDate() - offset);
      const key = date.toISOString().slice(0, 10);
      bucketMap.set(key, 0);
    }
    for (const item of items) {
      const key = new Date(item.updated_at).toISOString().slice(0, 10);
      if (bucketMap.has(key)) {
        bucketMap.set(key, (bucketMap.get(key) ?? 0) + item.total_tokens);
      }
    }
    return Array.from(bucketMap.entries()).map(([key, total]) => ({
      label: key.slice(5),
      total
    }));
  }

  function buildBreakdown(items: ConversationListItem[]): Array<{ label: string; total: number }> {
    const totals = new Map<string, number>();
    for (const item of items) {
      totals.set(item.provider, (totals.get(item.provider) ?? 0) + item.total_tokens);
    }
    return Array.from(totals.entries())
      .map(([label, total]) => ({ label, total }))
      .sort((a, b) => b.total - a.total);
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Statistics / Tokens</span>
    <h1 class="page-title">Token and workload analysis across users, providers, and recent conversations</h1>
    <p class="page-description">
      Filter by user or provider, inspect recent usage pressure, and identify which conversation chains are driving the most token burn on the selected target.
    </p>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required to inspect token statistics.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading token analytics...</div>
  {:else}
    <section class="card">
      <div class="filters three-up">
        <label class="field">
          <span class="field-label">Time range</span>
          <Select.Root type="single" bind:value={range} items={rangeItems}>
            <Select.Trigger class="select-trigger">{rangeItems.find((item) => item.value === range)?.label ?? 'Select range'}</Select.Trigger>
            <Select.Portal>
              <Select.Content class="select-content" sideOffset={8}>
                <Select.Viewport>
                  {#each rangeItems as item}
                    <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                  {/each}
                </Select.Viewport>
              </Select.Content>
            </Select.Portal>
          </Select.Root>
        </label>
        <label class="field">
          <span class="field-label">User filter</span>
          <Select.Root type="single" bind:value={selectedUserId} items={userItems}>
            <Select.Trigger class="select-trigger">{userItems.find((item) => item.value === selectedUserId)?.label ?? 'All users'}</Select.Trigger>
            <Select.Portal>
              <Select.Content class="select-content" sideOffset={8}>
                <Select.Viewport>
                  {#each userItems as item}
                    <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                  {/each}
                </Select.Viewport>
              </Select.Content>
            </Select.Portal>
          </Select.Root>
        </label>
        <label class="field">
          <span class="field-label">Provider filter</span>
          <Select.Root type="single" bind:value={selectedProvider} items={providerItems}>
            <Select.Trigger class="select-trigger">{providerItems.find((item) => item.value === selectedProvider)?.label ?? 'All providers'}</Select.Trigger>
            <Select.Portal>
              <Select.Content class="select-content" sideOffset={8}>
                <Select.Viewport>
                  {#each providerItems as item}
                    <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                  {/each}
                </Select.Viewport>
              </Select.Content>
            </Select.Portal>
          </Select.Root>
        </label>
      </div>
    </section>

    <div class="kpi-grid">
      <KpiCard label="Filtered Tokens" value={totalTokens.toLocaleString()} meta="Sum of visible conversations" />
      <KpiCard label="Conversations" value={String(filteredConversations.length)} meta="Sessions within current filters" />
      <KpiCard label="Active Users" value={String(activeUsers)} meta="Distinct users in result set" />
      <KpiCard label="Avg Tokens" value={averageTokens.toLocaleString()} meta="Average per visible conversation" />
    </div>

    <div class="grid-2">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Trend</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Daily token totals</h2>
          </div>
        </div>
        <EChart option={trendOption} height={300} />
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Providers</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Provider breakdown</h2>
          </div>
        </div>
        <EChart option={providerOption} height={300} />
      </section>
    </div>

    <section class="table-card">
      <div class="card" style="border: none; box-shadow: none; border-radius: 0;">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Top conversations</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Highest token sessions in range</h2>
          </div>
        </div>
      </div>
      <div class="table-scroll">
        <table class="data-table">
          <thead>
            <tr>
              <th>Conversation</th>
              <th>User</th>
              <th>Provider</th>
              <th>Tokens</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {#each topConversations as item}
              <tr>
                <td>
                  <a href={`/conversations/${item.conversation_id}?user_id=${item.user_id}`}>
                    <strong>{item.conversation_id}</strong>
                  </a>
                  <div class="subtle-text">{item.protocol} · {item.mode}</div>
                </td>
                <td>{item.user_id}</td>
                <td>{item.provider}</td>
                <td>{item.total_tokens.toLocaleString()}</td>
                <td>{new Date(item.updated_at).toLocaleString()}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
</section>
