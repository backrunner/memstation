import { workspaceRead } from '#lib/server/workspace-read.ts';
import type { ConversationRecord, ConversationMemoryPayload } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async event => {
  const { workspaceResult } = await event.parent();
  const path = `/conversations/${encodeURIComponent(event.params.id)}`;
  const record = await workspaceRead<ConversationRecord>(event, workspaceResult, path);
  // Never request related collections for an inaccessible conversation.
  const memories = record.value ? await workspaceRead<ConversationMemoryPayload>(event, workspaceResult, `${path}/memory`) : { value: null, failure: '', status: record.status };
  return { record, memories, generation: event.url.searchParams.get('generation_id') ?? '' };
};
