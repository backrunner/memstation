import type { LoadEvent, ServerLoadEvent } from '@sveltejs/kit';

// Use the same authenticated, destination-pinned BFF for SSR and browser calls.
// Returning a scoped failure keeps reconnect/retry visible without selecting a
// different service or silently treating an older server as an empty directory.
export async function workspaceRequest<T>(fetcher: LoadEvent['fetch'] | ServerLoadEvent['fetch'], targetId: string, path: string, init?: RequestInit): Promise<{ value: T | null; failure: string; status: number }> {
  try {
    const response = await fetcher(`/manager/api/targets/${encodeURIComponent(targetId)}/api/admin/workspaces${path}`, { ...init, cache: 'no-store' });
    if (response.ok) return { value: await response.json() as T, failure: '', status: response.status };
    const failure = response.status === 401 ? 'Reconnect this service to view its workspaces.'
      : response.status === 404 ? 'Workspace management is unavailable, or this workspace no longer exists.'
      : 'Unable to load this workspace scope. Retry when the service is available.';
    return { value: null, failure, status: response.status };
  } catch { return { value: null, failure: 'Unable to reach this service. Check the connection and retry.', status: 502 }; }
}
