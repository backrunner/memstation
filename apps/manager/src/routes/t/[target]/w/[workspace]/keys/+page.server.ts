import { readRecordPage } from '#lib/server/record-page.ts';
import type { KeyMemoryRecord } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  return readRecordPage<KeyMemoryRecord>(event, workspaceResult, '/memory/keys/page');
};
