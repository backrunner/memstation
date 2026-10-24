import { workspaceDocumentKinds } from '#lib/scope/routes.ts';
import { workspaceRead } from '#lib/server/workspace-read.ts';
import type { MemoryDocument } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  const kind = event.url.searchParams.get('kind') ?? '';
  const query = event.url.searchParams.get('q') ?? '';
  const after = event.url.searchParams.get('after') ?? '';
  const invalid = ['q', 'kind', 'after'].some(key => event.url.searchParams.getAll(key).length > 1);
  return { after, records: invalid ? { value: null, failure: 'Choose one value per memory filter.', status: 400 } : await workspaceRead<{ items: MemoryDocument[]; next_cursor: string; generation_id: string }>(event, workspaceResult, '/memory/documents/page', new URLSearchParams({ kind: kind || workspaceDocumentKinds.join(','), q: query, after, limit: '30' })), kind, query, generation: event.url.searchParams.get('generation_id') ?? '' };
};
