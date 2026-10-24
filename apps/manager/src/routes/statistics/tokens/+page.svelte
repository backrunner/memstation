<svelte:options runes={false} />

<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import { workspaceEntryURL } from '#lib/scope/navigation.ts';
  import SelectField from '#lib/components/SelectField.svelte';
  import { onMount } from 'svelte';

  import { fetchConversations, fetchUsers } from '#lib/api/client.ts';
  import EChart from '#lib/components/EChart.svelte';
  import KpiCard from '#lib/components/KpiCard.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { ConversationListItem, UserSummary } from '#lib/types.ts';

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
    ...users.map((user) => ({ value: user.user_id, label: `${user.name} (${user.user_id})` }))
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
    tooltip: { trigger: 'axis', backgroundColor: '#fffefb', borderColor: '#d9ded7', textStyle: { color: '#1b2821' } },
    grid: { left: 44, right: 16, top: 16, bottom: 32 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: trendBuckets.map((item) => item.label),
      axisLine: { lineStyle: { color: '#d9ded7' } },
      axisTick: { show: false },
      axisLabel: { color: '#6a776e', fontSize: 11 }
    },
    yAxis: {
      type: 'value',
      axisLine: { show: false },
      splitLine: { lineStyle: { color: '#e8ece6' } },
      axisLabel: { color: '#6a776e', fontSize: 11 }
    },
    series: [
      {
        type: 'line',
        smooth: true,
        showSymbol: false,
        data: trendBuckets.map((item) => item.total),
        lineStyle: { color: '#0b7059', width: 2 },
        itemStyle: { color: '#0b7059' }
      }
    ]
  };

  $: providerBreakdown = buildBreakdown(filteredConversations);
  $: providerOption = {
    tooltip: { trigger: 'axis', backgroundColor: '#fffefb', borderColor: '#d9ded7', textStyle: { color: '#1b2821' } },
    grid: { left: 44, right: 16, top: 16, bottom: 32 },
    xAxis: {
      type: 'category',
      data: providerBreakdown.map((item) => item.label),
      axisLine: { lineStyle: { color: '#d9ded7' } },
      axisTick: { show: false },
      axisLabel: { color: '#6a776e', fontSize: 11 }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#e8ece6' } },
      axisLabel: { color: '#6a776e', fontSize: 11 }
    },
    series: [
      {
        type: 'bar',
        data: providerBreakdown.map((item) => item.total),
        itemStyle: { color: '#0b7059', borderRadius: [3, 3, 0, 0] },
        barWidth: '48%'
      }
    ]
  };

  function buildTrend(items: ConversationListItem[], days: number): Array<{ label: string; total: number }> {
    const bucketMap = new Map<string, number>();
    for (let offset = days - 1; offset >= 0; offset -= 1) {
      const date = new Date();
      date.setHours(0, 0, 0, 0);
      date.setDate(date.getDate() - offset);
      const key = localDateKey(date);
      bucketMap.set(key, 0);
    }
    for (const item of items) {
      const key = localDateKey(new Date(item.updated_at));
      if (bucketMap.has(key)) {
        bucketMap.set(key, (bucketMap.get(key) ?? 0) + item.total_tokens);
      }
    }
    return Array.from(bucketMap.entries()).map(([key, total]) => ({
      label: key.slice(5),
      total
    }));
  }

  function localDateKey(date: Date): string {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
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
  <PageHeader title="Token usage" />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to inspect token statistics.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading token analytics…</div>
  {:else}
    <section class="filter-bar">
      <div class="filters three-up">
        <SelectField label="Time range" bind:value={range} items={rangeItems} />
        <SelectField label="User filter" bind:value={selectedUserId} items={userItems} />
        <SelectField label="Provider filter" bind:value={selectedProvider} items={providerItems} />
      </div>
    </section>

    <div class="kpi-grid summary-metrics">
      <KpiCard label="Filtered tokens" value={totalTokens.toLocaleString()} />
      <KpiCard label="Conversations" value={String(filteredConversations.length)} />
      <KpiCard label="Active users" value={String(activeUsers)} />
      <KpiCard label="Avg tokens / session" value={averageTokens.toLocaleString()} />
    </div>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Daily token totals</h2>
          </div>
        </div>
        <EChart option={trendOption} height={260} />
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Provider breakdown</h2>
          </div>
        </div>
        <EChart option={providerOption} height={260} />
      </section>
    </div>

    <section class="card is-flush">
      <div class="card-header">
        <div class="page-header-meta">
          <h2 class="section-title">Top conversations by tokens</h2>
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
                  <a href={workspaceEntryURL($selectedTarget?.id, `/conversations/${encodeURIComponent(item.conversation_id)}?user_id=${encodeURIComponent(item.user_id)}`)}>
                    <strong class="mono">{item.conversation_id}</strong>
                  </a>
                  <MetadataRow items={[item.protocol, item.mode]} />
                </td>
                <td class="mono">{item.user_id}</td>
                <td class="mono">{item.provider}</td>
                <td class="mono">{item.total_tokens.toLocaleString()}</td>
                <td class="subtle-text no-wrap">{new Date(item.updated_at).toLocaleString()}</td>
              </tr>
            {/each}
            {#if topConversations.length === 0}
              <tr>
                <td colspan="5">
                  <div class="empty-state" style="border: none; padding: 12px 0;">No conversations in range.</div>
                </td>
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
</section>
