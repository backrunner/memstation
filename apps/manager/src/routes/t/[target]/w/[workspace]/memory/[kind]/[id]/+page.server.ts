import { workspaceRead } from '#lib/server/workspace-read.ts';
import type { MemoryDocument } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  return { record: await workspaceRead<MemoryDocument>(event, workspaceResult, `/memory/documents/${encodeURIComponent(event.params.kind)}/${encodeURIComponent(event.params.id)}`), generation: event.url.searchParams.get('generation_id') ?? '' };
};
