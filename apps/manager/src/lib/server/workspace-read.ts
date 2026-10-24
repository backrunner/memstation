import type { ServerLoadEvent } from '@sveltejs/kit';
import type { WorkspaceRecord } from '#lib/types.ts';
import { workspaceRequest } from './workspaces.ts';

export interface WorkspaceReadResult<T> { value: T | null; failure: string; status: number }

export async function workspaceRead<T>(event: Pick<ServerLoadEvent, 'fetch' | 'params' | 'url' | 'depends'>, workspace: WorkspaceReadResult<WorkspaceRecord>, path: string, query = new URLSearchParams(), init?: RequestInit): Promise<WorkspaceReadResult<T>> {
  event.depends('workspace:read');
  if (!workspace.value) return { ...workspace, value: null };
  for (const [key, expected] of [['user_id', workspace.value.user_id], ['workspace_id', workspace.value.id]]) {
    const values = event.url.searchParams.getAll(key);
    if (values.length > 1 || values.some(value => value !== expected)) return { value: null, failure: 'This link does not match the selected workspace.', status: 404 };
  }
  const generations = event.url.searchParams.getAll('generation_id');
  if (generations.length > 1) return { value: null, failure: 'Choose one memory generation.', status: 400 };
  if (generations[0]) query.set('generation_id', generations[0]);
  const result = await workspaceRequest<T>(event.fetch, event.params.target!, `/${encodeURIComponent(workspace.value.id)}${path}?${query}`, init);
  if (result.status === 404) return { ...result, failure: 'This record is unavailable in this workspace, or this service does not support workspace browsing.' };
  if (result.status === 401) return { ...result, failure: 'Reconnect this service to view workspace records.' };
  if (result.status === 400) return { ...result, failure: 'Invalid filters or page link. Check the filters or return to the first page.' };
  if (result.failure) return { ...result, failure: 'Unable to load workspace records. Check the service and retry.' };
  return result;
}
