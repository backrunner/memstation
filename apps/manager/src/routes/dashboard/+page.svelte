<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import { fetchAudit, fetchConversations, fetchOverview, fetchQueues, fetchUsers } from '$lib/api/client';
  import EChart from '$lib/components/EChart.svelte';
  import KpiCard from '$lib/components/KpiCard.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import StatusBadge from '$lib/components/StatusBadge.svelte';
  import { selectedTarget, targetState } from '$lib/stores/targets';
  import type {
    AuditEventRecord,
    ConversationListItem,
    DashboardOverview,
    QueueInfo,
    UserSummary
  } from '$lib/types';

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
  $: keyMemoryTotal = users.reduce((sum, user) => sum + (user.key_memory_count ?? 0), 0);
  $: recentConversations = conversations.filter(
    (item) => Date.now() - new Date(item.updated_at).getTime() <= range * 24 * 60 * 60 * 1000
  );
  $: tokenTrend = buildTrend(recentConversations, range);
  $: tokenTrendOption = {
    tooltip: { trigger: 'axis', backgroundColor: '#ffffff', borderColor: '#d6e0ea', textStyle: { color: '#102033' } },
    grid: { left: 44, right: 16, top: 16, bottom: 32 },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: tokenTrend.map((item) => item.label),
      axisLine: { lineStyle: { color: '#d6e0ea' } },
      axisTick: { show: false },
      axisLabel: { color: '#5a6b7d', fontSize: 11 }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#e3eaf2' } },
      axisLabel: { color: '#5a6b7d', fontSize: 11 }
    },
    series: [
      {
        type: 'line',
        smooth: true,
        showSymbol: false,
        data: tokenTrend.map((item) => item.total),
        lineStyle: { color: '#1e5aa8', width: 2 },
        areaStyle: {
          color: {
            type: 'linear',
            x: 0, y: 0, x2: 0, y2: 1,
            colorStops: [
              { offset: 0, color: 'rgba(30, 90, 168, 0.18)' },
              { offset: 1, color: 'rgba(30, 90, 168, 0.02)' }
            ]
          }
        },
        itemStyle: { color: '#1e5aa8' }
      }
    ]
  };

  $: userFootprint = [...users].sort((a, b) => b.total_tokens - a.total_tokens).slice(0, 6);
  $: footprintOption = {
    tooltip: { trigger: 'axis', backgroundColor: '#ffffff', borderColor: '#d6e0ea', textStyle: { color: '#102033' } },
    grid: { left: 44, right: 16, top: 16, bottom: 48 },
    xAxis: {
      type: 'category',
      data: userFootprint.map((item) => item.name),
      axisLine: { lineStyle: { color: '#d6e0ea' } },
      axisTick: { show: false },
      axisLabel: { color: '#5a6b7d', fontSize: 11, rotate: 16 }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#e3eaf2' } },
      axisLabel: { color: '#5a6b7d', fontSize: 11 }
    },
    series: [
      {
        type: 'bar',
        data: userFootprint.map((item) => item.total_tokens),
        itemStyle: { color: '#1e5aa8', borderRadius: [4, 4, 0, 0] },
        barWidth: '48%'
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
  <PageHeader
    eyebrow="Overview"
    title="Dashboard"
    description="Signal of live load, queue posture, and recent sensitive activity for the selected target."
  />

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target before dashboard data can be loaded.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading dashboard metrics…</div>
  {:else}
    <div class="kpi-grid six-up">
      <KpiCard
        label="Active Conversations"
        value={String(overview?.active_conversations ?? 0)}
        href="/conversations"
      />
      <KpiCard
        label="Stored Memories"
        value={String(overview?.memory_count ?? 0)}
        href="/memory"
      />
      <KpiCard
        label="Key Memories"
        value={String(keyMemoryTotal)}
        href="/memory/keys"
      />
      <KpiCard
        label="Pending Tasks"
        value={String(pendingTasks)}
        meta={`${retryTasks} in retry`}
        href="/operations/queues"
      />
      <KpiCard
        label="Audit Events"
        value={String(auditItems.length)}
        meta="Last 100 actions"
        href="/audit"
      />
      <KpiCard
        label="Total Tokens"
        value={totalTokens.toLocaleString()}
        meta="Sum across users"
        href="/statistics/tokens"
      />
    </div>

    <div class="grid-2">
      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Token load</h2>
            <p class="panel-meta">Total tokens by day across active users.</p>
          </div>
          <div class="segmented-control" role="tablist" aria-label="Range">
            {#each [7, 30] as item}
              <button
                class="segmented-control-item"
                type="button"
                data-state={range === item ? 'on' : 'off'}
                on:click={() => (range = item)}
              >{item}d</button>
            {/each}
          </div>
        </div>
        <EChart option={tokenTrendOption} height={260} />
      </section>

      <section class="card page-stack">
        <div class="page-header">
          <div class="page-header-meta">
            <h2 class="section-title">Top users by tokens</h2>
            <p class="panel-meta">Lifetime tokens per user, top six.</p>
          </div>
        </div>
        <EChart option={footprintOption} height={260} />
      </section>
    </div>

    <div class="grid-2">
      <section class="card is-flush">
        <div class="card-header">
          <div class="page-header-meta">
            <h2 class="section-title">Worker queues</h2>
            <p class="panel-meta">Current depth across durable worker queues.</p>
          </div>
          <a class="btn-secondary btn-sm" href="/operations/queues">Queue details</a>
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
                    <a href={`/operations/queues/${encodeURIComponent(queue.name)}`}>
                      <strong class="mono">{queue.name}</strong>
                    </a>
                  </td>
                  <td class="mono">{queue.pending}</td>
                  <td class="mono">{queue.active}</td>
                  <td class="mono">
                    {#if queue.retry > 0}
                      <StatusBadge tone="warning" label={String(queue.retry)} />
                    {:else}
                      {queue.retry}
                    {/if}
                  </td>
                  <td class="mono">{queue.latency_ms ?? 0} ms</td>
                </tr>
              {/each}
              {#if queues.length === 0}
                <tr>
                  <td colspan="5">
                    <div class="empty-state" style="border: none; padding: 12px 0;">No queue metrics returned.</div>
                  </td>
                </tr>
              {/if}
            </tbody>
          </table>
        </div>
      </section>

      <section class="card is-flush">
        <div class="card-header">
          <div class="page-header-meta">
            <h2 class="section-title">Recent sensitive actions</h2>
            <p class="panel-meta">Audited operations on this target.</p>
          </div>
          <a class="btn-secondary btn-sm" href="/audit">Open audit</a>
        </div>
        {#if auditItems.length === 0}
          <div class="card-body">
            <div class="empty-state">No audit events are available for this target yet.</div>
          </div>
        {:else}
          <div class="table-scroll">
            <table class="data-table">
              <thead>
                <tr>
                  <th>Action</th>
                  <th>Target</th>
                  <th>Actor</th>
                  <th>When</th>
                </tr>
              </thead>
              <tbody>
                {#each auditItems.slice(0, 8) as item}
                  <tr class="is-selectable">
                    <td>
                      <a
                        href={`/audit?action=${encodeURIComponent(item.action)}&target_type=${encodeURIComponent(item.target_type)}&target_id=${encodeURIComponent(item.target_id)}`}
                      >
                        <strong class="mono">{item.action}</strong>
                      </a>
                    </td>
                    <td class="mono">
                      <span>{item.target_type}</span>
                      <div class="subtle-text">{item.target_id}</div>
                    </td>
                    <td class="subtle-text">{item.actor_type ?? 'system'}</td>
                    <td class="subtle-text no-wrap">{item.created_at ? new Date(item.created_at).toLocaleString() : 'n/a'}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    </div>
  {/if}
</section>
