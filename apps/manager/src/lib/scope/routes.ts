import type { MemStationTarget, WorkspaceRecord } from '#lib/types.ts';

export interface ScopeContext { target: MemStationTarget; workspace: WorkspaceRecord }
export function workspaceDirectoryURL(targetId: string): string {
  return `/t/${encodeURIComponent(targetId)}/workspaces`;
}
export function workspaceURL(targetId: string, workspaceId: string): string {
  return `${workspaceBaseURL(targetId, workspaceId)}/settings`;
}
export function workspaceBaseURL(targetId: string, workspaceId: string): string {
  return `/t/${encodeURIComponent(targetId)}/w/${encodeURIComponent(workspaceId)}`;
}
export function workspaceMemoryURL(base: string, kind: string, id: string, generation?: string): string {
  if (kind === 'key_memory') return `${base}/keys/${encodeURIComponent(id)}`;
  return `${base}/memory/${encodeURIComponent(kind)}/${encodeURIComponent(id)}${generation ? `?generation_id=${encodeURIComponent(generation)}` : ''}`;
}
export function workspaceConversationURL(base: string, id: string, generation?: string): string {
  return `${base}/conversations/${encodeURIComponent(id)}${generation ? `?generation_id=${encodeURIComponent(generation)}` : ''}`;
}
export function workspaceDraftKey(scope: ScopeContext): string {
  return JSON.stringify(['workspace-settings', scope.target.id, scope.target.baseUrl, scope.workspace.user_id, scope.workspace.id]);
}

export const workspaceDocumentKinds = ['summary', 'segment', 'fact', 'impression', 'event', 'task', 'procedure', 'artifact', 'entity', 'agent_action'] as const;

// Record IDs, generations, cursors and owners never transfer between workspaces.
export function switchWorkspaceURL(current: { pathname: string; searchParams: Pick<URLSearchParams, 'get'> }, targetId: string, workspaceId: string): string {
  const tail = current.pathname.split('/w/')[1]?.split('/').slice(1).join('/') ?? '';
  const section = tail.startsWith('memory/search') ? 'memory/search' : ['memory', 'keys', 'conversations', 'access', 'settings'].find(item => tail === item || tail.startsWith(`${item}/`)) ?? 'memory';
  const allowed = section === 'memory/search' ? ['q', 'kind', 'top_n', 'as_of', 'known_at', 'date_from', 'date_to'] : section === 'memory' ? ['q', 'kind'] : ['keys', 'conversations'].includes(section) ? ['q'] : [];
  const query = new URLSearchParams();
  for (const key of allowed) { const value = current.searchParams.get(key); if (value) query.set(key, value); }
  return `${workspaceBaseURL(targetId, workspaceId)}/${section}${query.size ? `?${query}` : ''}`;
}
