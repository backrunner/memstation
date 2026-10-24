import { workspaceRead, type WorkspaceReadResult } from '#lib/server/workspace-read.ts';
import type { MemoryDocument } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

interface SearchResult { memories: MemoryDocument[]; total: number }
export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  const fields = ['q', 'kind', 'top_n', 'as_of', 'known_at', 'date_from', 'date_to'] as const;
  const filters = Object.fromEntries(fields.map(key => [key, event.url.searchParams.get(key) ?? (key === 'top_n' ? '10' : '')])) as Record<typeof fields[number], string>;
  const submitted = event.url.searchParams.has('q');
  let result: WorkspaceReadResult<SearchResult> = { value: null, failure: '', status: 200 };
  if (fields.some(key => event.url.searchParams.getAll(key).length > 1)) result = { value: null, failure: 'Choose one value per search filter.', status: 400 };
  else if ((filters.as_of || filters.known_at) && filters.kind && filters.kind !== 'fact') result = { value: null, failure: 'History is available for facts only. Choose fact or all types.', status: 400 };
  else if (submitted) {
    result = await workspaceRead<SearchResult>(event, workspaceResult, '/memory/search', new URLSearchParams(), {
      method: 'POST', headers: { 'Content-Type': 'application/json', Origin: event.url.origin },
      body: JSON.stringify({ query: filters.q, top_n: Number(filters.top_n), memory_types: filters.kind ? [filters.kind] : [], as_of: filters.as_of, known_at: filters.known_at, date_from: filters.date_from, date_to: filters.date_to })
    });
  }
  return { filters, result, submitted, generation: event.url.searchParams.get('generation_id') ?? '', historical: Boolean(filters.as_of || filters.known_at) };
};
