import type { ServerLoadEvent } from '@sveltejs/kit';
import type { WorkspaceRecord } from '#lib/types.ts';
import { workspaceRead, type WorkspaceReadResult } from './workspace-read.ts';

export interface RecordPage<T> { items: T[]; next_cursor: string }
export async function readRecordPage<T>(event: Pick<ServerLoadEvent, 'fetch' | 'params' | 'url' | 'depends'>, workspace: WorkspaceReadResult<WorkspaceRecord>, path: string) {
  const query = event.url.searchParams.get('q') ?? '';
  const after = event.url.searchParams.get('after') ?? '';
  const invalid = ['q', 'after'].some(key => event.url.searchParams.getAll(key).length > 1) || event.url.searchParams.has('generation_id');
  const records: WorkspaceReadResult<RecordPage<T>> = invalid
    ? { value: null, failure: 'Invalid filters or page link. Return to the first page and retry.', status: 400 }
    : await workspaceRead<RecordPage<T>>(event, workspace, path, new URLSearchParams({ q: query, after, limit: '30' }));
  return { records, query, after, invalid };
}
