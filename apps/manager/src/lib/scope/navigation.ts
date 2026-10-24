import { workspaceBaseURL } from './routes.ts';

const kinds: Record<string, string> = { summaries: 'summary', impressions: 'impression', facts: 'fact', events: 'event', tasks: 'task', procedures: 'procedure', artifacts: 'artifact', entities: 'entity', 'agent-actions': 'agent_action', agent_actions: 'agent_action' };

// Accept only known legacy records, never a redirect URL or arbitrary path.
export function workspaceDestination(base: string, href: string): string | null {
  const [path, search = ''] = href.split('?');
  const parts = path.split('/').filter(Boolean);
  const query = new URLSearchParams(search);
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('#') || parts.some(p => p === '.' || p === '..') || [...query.keys()].some(k => query.getAll(k).length !== 1)) return null;
  let tail: string; const result = new URLSearchParams();
  if (parts[0] === 'conversations' && parts.length <= 2) {
    tail = parts.join('/');
  } else if (parts[0] === 'memory' && parts.length <= 3) {
    const kind = parts[1];
    if (kind === 'keys') tail = ['keys', ...parts.slice(2)].join('/');
    else if (kind && kinds[kind]) {
      tail = parts[2] ? `memory/${kinds[kind]}/${parts[2]}` : 'memory';
      if (!parts[2]) result.set('kind', kinds[kind]);
    } else if (!kind || kind === 'items' || kind === 'search') {
      if (parts.length > 2) return null;
      tail = kind === 'search' || (!kind && (query.has('q') || query.has('as_of') || query.has('known_at'))) ? 'memory/search' : 'memory';
      const type = query.get('type');
      if (kind === 'items' && type) {
        if (!Object.values(kinds).includes(type)) return null;
        result.set('kind', type);
      }
    } else return null;
  } else return null;
  const allowed = tail === 'memory/search' ? ['q', 'kind', 'top_n', 'as_of', 'known_at', 'date_from', 'date_to', 'generation_id'] : ['q', 'generation_id'];
  for (const key of allowed) { const value = query.get(key); if (value) result.set(key, value); }
  if (query.has('workspace_id') || query.has('after') || (tail.startsWith('keys') && query.has('generation_id'))) return null;
  return `${base}/${tail}${result.size ? `?${result}` : ''}`;
}

export function workspaceEntryURL(targetId: string | undefined, href: string, workspaceId?: string): string {
  if (!targetId || !workspaceDestination('', href)) return href;
  if (workspaceId) return workspaceDestination(workspaceBaseURL(targetId, workspaceId), href)!;
  return `/t/${encodeURIComponent(targetId)}/open${href}`;
}

// Advanced audit/rebuild screens remain available during the route migration.
// Pin their service explicitly so a shared link cannot use a saved other target.
export function legacyReviewURL(targetId: string, path: string, owner: string, generation?: string): string {
  const query = new URLSearchParams({ manager_target: targetId, user_id: owner });
  if (generation) query.set('generation_id', generation);
  return `${path}?${query}`;
}
