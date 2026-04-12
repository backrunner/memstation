<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchAudit, fetchConversations, fetchOverview, fetchQueues, fetchUsers } from '$lib/api/client';
  import EChart from '$lib/components/EChart.svelte';
  import KpiCard from '$lib/components/KpiCard.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type { AuditEventRecord, ConversationListItem, DashboardOverview, QueueInfo, UserSummary } from '$lib/types';

  let loading = true;
  let error = '';
  let lastLoadKey = '';
  let range = 7;
  let overview: DashboardOverview | null = null;
  let queues: QueueInfo[] = [];
  let auditItems: AuditEventRecord[] = [];
  let users: UserSummary[] = [];
  let conversations: ConversationListItem[] = [];

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
      [overview, queues, auditItems, users] = await Promise.all([
        fetchOverview($selectedTarget),
        fetchQueues($selectedTarget),
        fetchAudit($selectedTarget),
        fetchUsers($selectedTarget)
      ]);
      const conversationLists = await Promise.all(
        users.slice(0, 8).map((user) => fetchConversations($selectedTarget, { userId: user.user_id, limit: 25 }))
      );
      conversations = conversationLists.flat();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load dashboard';
    } finally {
      loading = false;
    }
  }

  $: totalTokens = users.reduce((sum, user) => sum + user.total_tokens, 0);
  $: pendingTasks = queues.reduce((sum, item) => sum + (item.pending ?? 0), 0);
  $: retryTasks = queues.reduce((sum, item) => sum + (item.retry ?? 0), 0);
  $: userCount = users.length;

  $: recentConversations = conversations.filter((item) => Date.now() - new Date(item.updated_at).getTime() <= range * 24 * 60 * 60 * 1000);
  $: tokenTrend = buildTrend(recentConversations, range);
  $: tokenTrendOption = {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 18, top: 24, bottom: 36 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: tokenTrend.map((item) => item.label),
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
        type: 'line',
        smooth: true,
        data: tokenTrend.map((item) => item.total),
        lineStyle: { color: '#1e5aa8', width: 3 },
        areaStyle: { color: 'rgba(30, 90, 168, 0.16)' },
        itemStyle: { color: '#1e5aa8' }
      }
    ]
  };

  $: userFootprint = [...users].sort((a, b) => b.total_tokens - a.total_tokens).slice(0, 6);
  $: footprintOption = {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 18, top: 24, bottom: 56 },
    xAxis: {
      type: 'category',
      data: userFootprint.map((item) => item.name),
      axisLine: { lineStyle: { color: '#c8d4df' } },
      axisLabel: { color: '#5a6b7d', rotate: 18 }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#e1e9f0' } },
      axisLabel: { color: '#5a6b7d' }
    },
    series: [
      {
        type: 'bar',
        data: userFootprint.map((item) => item.total_tokens),
        itemStyle: {
          color: '#2f855a',
          borderRadius: [10, 10, 0, 0]
        }
      }
    ]
  };

  function buildTrend(items: ConversationListItem[], days: number): Array<{ label: string; total: number }> {
    const buckets = new Map<string, number>();
    for (let offset = days - 1; offset >= 0; offset -= 1) {
      const date = new Date();
      date.setHours(0, 0, 0, 0);
      date.setDate(date.getDate() - offset);
      const key = localDateKey(date);
      buckets.set(key, 0);
    }
    for (const item of items) {
      const key = localDateKey(new Date(item.updated_at));
      if (buckets.has(key)) {
        buckets.set(key, (buckets.get(key) ?? 0) + item.total_tokens);
      }
    }
    return Array.from(buckets.entries()).map(([key, total]) => ({ label: key.slice(5), total }));
  }

  function localDateKey(date: Date): string {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Dashboard</span>
    <h1 class="page-title">Overview</h1>
  </div>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Admin login is required before dashboard data can be loaded from the selected target.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading dashboard metrics...</div>
  {:else}
    <div class="kpi-grid">
      <KpiCard label="Active Conversations" value={String(overview?.active_conversations ?? 0)} />
      <KpiCard label="Stored Memories" value={String(overview?.memory_count ?? 0)} />
      <KpiCard label="Pending Tasks" value={String(pendingTasks)} />
      <KpiCard label="Total Tokens" value={totalTokens.toLocaleString()} />
    </div>

    <div class="summary-strip">
      <div class="summary-chip"><strong>{userCount}</strong> users</div>
      <div class="summary-chip"><strong>{overview?.key_memory_count ?? 0}</strong> key memories</div>
      <div class="summary-chip"><strong>{retryTasks}</strong> retries</div>
    </div>

    <div class="grid-2">
      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Traffic</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Recent token pressure</h2>
          </div>
          <div class="inline-group">
            {#each [7, 30] as item}
              <button class={range === item ? 'button-secondary' : 'button-ghost'} type="button" on:click={() => (range = item)}>{item}d</button>
            {/each}
          </div>
        </div>
        <EChart option={tokenTrendOption} height={300} />
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Footprint</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Top user load</h2>
          </div>
        </div>
        <EChart option={footprintOption} height={300} />
      </section>
    </div>

    <div class="grid-2">
      <section class="table-card">
        <div class="card" style="border: none; box-shadow: none; border-radius: 0;">
          <div class="page-header">
          <div>
            <div class="sidebar-label">Queues</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Worker lanes</h2>
          </div>
          <div class="toolbar">
            <a class="button-secondary" href="/operations/queues">Queue details</a>
          </div>
        </div>
        </div>
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>Queue</th>
                <th>Pending</th>
                <th>Active</th>
                <th>Retry</th>
                <th>Latency</th>
              </tr>
            </thead>
            <tbody>
              {#each queues as queue}
                <tr>
                  <td>
                    <div class="inline-group" style="justify-content: space-between; flex-wrap: wrap;">
                      <span>{queue.name}</span>
                      <a class="button-ghost" href={`/operations/queues/${encodeURIComponent(queue.name)}`}>Open</a>
                    </div>
                  </td>
                  <td>{queue.pending}</td>
                  <td>{queue.active}</td>
                  <td>{queue.retry}</td>
                  <td>{queue.latency_ms ?? 0} ms</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="card">
        <div class="page-header">
          <div>
            <div class="sidebar-label">Recent audit</div>
            <h2 class="page-title" style="font-size: 1.35rem;">Latest sensitive actions</h2>
          </div>
          <a class="button-secondary" href="/audit">Open audit</a>
        </div>
        {#if auditItems.length === 0}
          <div class="empty-state">No audit events are available for this target yet.</div>
        {:else}
          <div class="toolbar">
            {#each auditItems.slice(0, 6) as item}
              <div class="debug-block">
                <div class="inline-group" style="justify-content: space-between; margin-bottom: 8px;">
                  <strong>{item.action}</strong>
                  <span class="subtle-text">{item.target_type}</span>
                </div>
                <div class="helper-text">{item.target_id}</div>
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</section>
