import { workspaceRead } from '#lib/server/workspace-read.ts';
import type { KeyMemoryRecord } from '#lib/types.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  return { record: await workspaceRead<KeyMemoryRecord>(event, workspaceResult, `/memory/keys/${encodeURIComponent(event.params.id)}`) };
};
