import { workspaceRead } from '#lib/server/workspace-read.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  return { creation: await workspaceRead<{items: unknown[]}>(event, workspaceResult, '/memory/keys', new URLSearchParams({ limit: '1' })) };
};
