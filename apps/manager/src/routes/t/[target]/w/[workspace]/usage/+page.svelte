<script lang="ts">
  import { invalidate } from '$app/navigation';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import FilterToolbar from '#lib/components/layout/FilterToolbar.svelte';
  import Button from '#lib/components/ui/Button.svelte';
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import CollapsibleSection from '#lib/components/ui/CollapsibleSection.svelte';
  import EChart from '#lib/components/charts/EChart.svelte';
  import type { PageData } from './$types';

  let { data }: { data: PageData } = $props();
  let modelFilter = $derived(data.filters.model);
  let sinceFilter = $derived(data.filters.since);
  let untilFilter = $derived(data.filters.until);
  let stageFilter = $derived(data.filters.stage);
  let strategyFilter = $derived(data.filters.strategy);
  let statusFilter = $derived(data.filters.status);
  let partitionFilter = $derived(data.filters.partition_id);
  const strategies = [{ value: '', label: 'All strategies' }, ...['generative', 'lightweight', 'hybrid'].map(value => ({ value, label: value }))];
  const statuses = [{ value: '', label: 'All outcomes' }, ...['complete', 'failed', 'unknown'].map(value => ({ value, label: value }))];
  let groups = $derived(data.report.value?.groups ?? []);
  let totals = $derived(groups.reduce((sum, group) => ({ calls: sum.calls + group.calls, known: sum.known + (group.authority === 'local' ? group.calls : group.known_usage_calls), input: sum.input + group.input_tokens, output: sum.output + group.output_tokens, retries: sum.retries + group.retries, unknown: sum.unknown + (group.status === 'unknown' ? group.calls : 0) }), { calls: 0, known: 0, input: 0, output: 0, retries: 0, unknown: 0 }));
  let stages = $derived.by(() => {
    const result = new Map<string, { stage: string; calls: number; input: number; output: number; duration: number }>();
    for (const group of groups) {
      const row = result.get(group.stage) ?? { stage: group.stage, calls: 0, input: 0, output: 0, duration: 0 };
      row.calls += group.calls; row.input += group.input_tokens; row.output += group.output_tokens; row.duration += group.duration_ms; result.set(group.stage, row);
    }
    return [...result.values()].sort((a, b) => b.calls - a.calls);
  });
  let chart = $derived.by(() => {
    const days = new Map<string, number>();
    for (const group of groups) days.set(group.day, (days.get(group.day) ?? 0) + group.input_tokens + group.output_tokens);
    const ordered = [...days].sort(([a], [b]) => a.localeCompare(b));
    return { tooltip: { trigger: 'axis' }, grid: { left: 64, right: 16, top: 24, bottom: 32 }, xAxis: { type: 'category', data: ordered.map(([day]) => day) }, yAxis: { type: 'value', name: 'Reported tokens' }, series: [{ type: 'bar', barMaxWidth: 44, data: ordered.map(([, tokens]) => tokens), itemStyle: { color: '#2563eb' } }] };
  });
  let calls = $derived(data.calls.value?.items ?? []);
  let next = $derived(calls.length === 100 ? `?${data.query}&after=${encodeURIComponent(calls.at(-1)!.id)}` : '');
  const number = (value: number) => new Intl.NumberFormat('en').format(value);
  const cost = (amount: string, currency: string) => `${currency} ${amount}`;
</script>

<svelte:head><title>Memory usage · MemStation</title></svelte:head>
<section class="page-stack">
  <PageHeader title="Memory usage" />
  <form method="GET" class="panel">
    <FilterToolbar activeCount={[stageFilter, strategyFilter, statusFilter, partitionFilter].filter(Boolean).length}>
      <TextInputField label="Model" name="model" bind:value={modelFilter} placeholder="All models" maxlength={256} />
      {#snippet actions()}<Button type="submit" class="btn-primary">Apply filters</Button><Button class="btn-secondary" onclick={() => invalidate('memory:usage')}>Refresh</Button>{/snippet}
      {#snippet filters()}
        <div class="filter-grid">
          <TextInputField label="From (UTC)" type="date" name="since" bind:value={sinceFilter} required />
          <TextInputField label="Through (UTC)" type="date" name="until" bind:value={untilFilter} required />
          <SelectField label="Strategy" name="strategy" bind:value={strategyFilter} items={strategies} />
          <SelectField label="Provider outcome" name="status" bind:value={statusFilter} items={statuses} />
          <TextInputField label="Stage" name="stage" bind:value={stageFilter} placeholder="All stages" maxlength={120} />
          <TextInputField label="Audience partition" name="partition_id" bind:value={partitionFilter} placeholder="Public workspace" />
        </div>
      {/snippet}
    </FilterToolbar>
  </form>
  {#if data.report.failure}
    <div class="error-state" role="alert">{data.report.failure}</div>
  {:else if data.report.value}
    <dl class="usage-totals panel">
      <div><dt>Calls</dt><dd>{number(totals.calls)}</dd></div>
      <div><dt>Input tokens</dt><dd>{number(totals.input)}</dd></div>
      <div><dt>Output tokens</dt><dd>{number(totals.output)}</dd></div>
      <div><dt>Job retry calls</dt><dd>{number(totals.retries)}</dd></div>
      <div><dt>Usage unreported</dt><dd>{number(totals.calls - totals.known)}</dd></div>
      <div><dt>Outcome unknown</dt><dd>{number(totals.unknown)}</dd></div>
    </dl>
    <section class="panel usage-section" aria-label="Reported costs">
      <h2>Reported costs</h2>
      {#each data.report.value.costs as item}<p class="cost-value">{cost(item.amount, item.currency)} <span class="helper-text">from {number(item.calls)} calls</span></p>{/each}
      {#if !data.report.value.costs.length}<p>No reported costs for this period.</p>{/if}
      <p class="helper-text">{number(data.report.value.unpriced_calls)} calls have no reported price. Missing usage and cost are not zero. Currencies remain separate. Broker charges are references and are not charged again by MemStation.</p>
    </section>
    {#if groups.length}
      <section class="panel"><h2>Daily reported tokens (UTC)</h2><EChart option={chart} height={240} /></section>
      <section class="panel"><h2>Processing stages</h2><div class="table-wrap"><table class="data-table"><thead><tr><th>Stage</th><th>Calls</th><th>Input</th><th>Output</th><th>Mean duration</th></tr></thead><tbody>{#each stages as row}<tr><td>{row.stage || 'Unclassified'}</td><td>{number(row.calls)}</td><td>{number(row.input)}</td><td>{number(row.output)}</td><td>{row.stage ? `${(row.duration / row.calls / 1000).toFixed(2)} s` : 'Unrecorded'}</td></tr>{/each}</tbody></table></div></section>
    {:else}<p class="empty-state">No calls match these filters.</p>{/if}
  {/if}
  {#if data.capabilities.value?.runtime.processing}
    {@const policy = data.capabilities.value.runtime.processing}
    <CollapsibleSection title="Current processing policy"><dl><dt>Strategy</dt><dd>{policy.strategy}</dd><dt>Entity query budget</dt><dd>{policy.entity_recall_timeout_ms} ms</dd><dt>Local model</dt><dd>{policy.model}</dd><dt>Revision</dt><dd class="mono">{policy.revision}</dd><dt>Hybrid audit rate</dt><dd>{Math.round(policy.audit_rate * 100)}%</dd><dt>Semantic input ceiling</dt><dd>{number(policy.max_semantic_bytes)} bytes</dd></dl><p class="helper-text">This is the effective deployment policy. Individual call records retain the strategy used at execution.</p></CollapsibleSection>
  {:else if data.capabilities.failure}<p class="error-state" role="alert">{data.capabilities.failure}</p>{/if}
  <section class="panel usage-section">
    <h2>Call records</h2><p class="helper-text">Provider outcomes describe inference requests. A completed request can still fail downstream memory validation. Job retry calls count requests made after a job retry; adapter invocations and truncation retries retain separate records.</p>
    {#if data.calls.failure}<p role="alert" class="error-state">{data.calls.failure}</p>
    {:else if calls.length}<div class="table-wrap"><table class="data-table"><thead><tr><th class="secondary-column">Time</th><th>Stage</th><th class="secondary-column">Strategy</th><th class="secondary-column">Model</th><th class="secondary-column">Outcome</th><th>Tokens (in / out)</th><th>Cost</th><th class="secondary-column">Duration</th></tr></thead><tbody>{#each calls as call (call.id)}<tr><td class="secondary-column">{new Date(call.created_at).toISOString().replace('T', ' ').slice(0, 19)} UTC</td><td class="call-stage">{call.stage || call.role}<span class="mobile-call-meta">{call.model_alias}</span><span class="mobile-call-meta">{call.status}</span></td><td class="secondary-column">{call.strategy || 'Unrecorded'}</td><td class="secondary-column" title={call.model_version}>{call.model_alias}</td><td class="secondary-column">{call.status}{#if call.failure_code}<span class="helper-text"> {call.failure_code}</span>{/if}</td><td>{call.usage_authority === 'local' ? 'Not applicable' : call.usage_known ? `${number(call.input_tokens)} / ${number(call.output_tokens)}` : 'Unreported'}</td><td>{call.reported_cost && call.currency ? cost(call.reported_cost, call.currency) : 'Unreported'}</td><td class="secondary-column">{call.stage ? `${(call.duration_ms / 1000).toFixed(2)} s` : 'Unrecorded'}</td></tr>{/each}</tbody></table></div>
      <div class="page-actions"><a href={`?${data.query}`} class="text-link">First page</a>{#if next}<a href={next} class="text-link">Next page</a>{/if}</div>
    {:else}<p class="empty-state">No call records in this page.</p>{/if}
  </section>
</section>

<style>
  .filter-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
  .usage-totals { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 16px; margin: 0; }
  dt { color: var(--text-muted); font-size: var(--font-size-sm); } dd { margin: 4px 0 12px; overflow-wrap: anywhere; }
  .usage-totals dd { font-size: 24px; margin-bottom: 0; font-variant-numeric: tabular-nums; }
  .cost-value { font-size: 20px; font-variant-numeric: tabular-nums; }
  .usage-section { display: grid; gap: 12px; } .usage-section p, .usage-section h2 { margin: 0; }
 .mobile-call-meta { display: none; }
 .table-wrap { overflow-x: auto; } th, td { white-space: nowrap; text-align: left; } h2 { font-size: var(--font-size-md); margin: 0 0 12px; }
  .page-actions { display: flex; gap: 20px; }
  @media(max-width: 1000px) { .usage-totals { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
  @media(max-width: 640px) { .secondary-column { display: none; } .mobile-call-meta { display: block; color: var(--text-muted); font-size: var(--font-size-xs); margin-top: 3px; } .data-table :is(th,td) { padding-inline: 8px; } .call-stage { white-space: normal; overflow-wrap: anywhere; max-width: 150px; } .filter-grid, .usage-totals { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
