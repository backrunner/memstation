import { workspaceRequest } from '#lib/server/workspaces.ts';
import type { AIUsageReport, BackgroundCall, MemoryCapabilities } from '#lib/memory-service.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ parent, fetch, params, url, depends }) => {
  await parent(); depends('memory:usage');
  const today = new Date();
  const since = url.searchParams.get('since') ?? new Date(today.getTime() - 29 * 86400000).toISOString().slice(0, 10);
  const until = url.searchParams.get('until') ?? today.toISOString().slice(0, 10);
  const filters = { since, until, stage: url.searchParams.get('stage') ?? '', strategy: url.searchParams.get('strategy') ?? '', status: url.searchParams.get('status') ?? '', model: url.searchParams.get('model') ?? '', partition_id: url.searchParams.get('partition_id') ?? '' };
  const start = new Date(`${since}T00:00:00Z`), end = new Date(`${until}T00:00:00Z`);
  const validDate = (value: string, date: Date) => /^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
  if (!validDate(since, start) || !validDate(until, end) || end < start || end.getTime() - start.getTime() >= 366 * 86400000) {
    return { filters, query: '', report: { value: null, failure: 'Choose a valid UTC date range of at most 366 days.', status: 400 }, calls: { value: null, failure: '', status: 400 }, capabilities: { value: null, failure: '', status: 400 } };
  }
  const query = new URLSearchParams({ ...filters, since: start.toISOString(), until: new Date(end.getTime() + 86400000).toISOString() });
  const base = `/${encodeURIComponent(params.workspace)}/lifecycle`;
  const page = new URLSearchParams(query); page.set('after', url.searchParams.get('after') ?? '');
  const [report, calls, capabilities] = await Promise.all([
    workspaceRequest<AIUsageReport>(fetch, params.target, `${base}/usage/summary?${query}`),
    workspaceRequest<{ items: BackgroundCall[] }>(fetch, params.target, `${base}/usage?${page}`),
    workspaceRequest<MemoryCapabilities>(fetch, params.target, `${base}/capabilities?${new URLSearchParams({ partition_id: filters.partition_id })}`)
  ]);
  return { filters, query: new URLSearchParams(filters).toString(), report, calls, capabilities };
};
