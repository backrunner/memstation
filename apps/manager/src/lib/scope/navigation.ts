import { workspaceBaseURL, workspaceDirectoryURL, workspaceDocumentKinds } from './routes.ts';

export type WorkspaceSection = 'memory' | 'memory/search' | 'keys' | 'conversations';
export interface WorkspaceDestination { section: WorkspaceSection; record?: string; kind?: string; generation?: string }
export function destinationQuery(destination: WorkspaceDestination): Record<string,string> {
  return Object.fromEntries(Object.entries(destination).filter(([,value]) => Boolean(value)));
}
export function readWorkspaceDestination(query: URLSearchParams): WorkspaceDestination | null {
  if (query.has('open') || query.has('manager_target')) throw new Error('Unsupported navigation parameter.');
  const section = query.get('section');
  if (!section) return null;
  if (!['memory','memory/search','keys','conversations'].includes(section)) throw new Error('Invalid workspace section.');
  for (const key of ['section','record','kind','generation']) if (query.getAll(key).length > 1) throw new Error('Choose one destination.');
  const record = query.get('record') || undefined;
  const kind = query.get('kind') || undefined;
  const generation = query.get('generation') || undefined;
  if ((kind && !workspaceDocumentKinds.includes(kind as typeof workspaceDocumentKinds[number])) || (record && section === 'memory' && !kind) || (record && section === 'memory/search') || (kind && section !== 'memory' && section !== 'memory/search') || (generation && section === 'keys')) throw new Error('Invalid workspace destination.');
  return {section: section as WorkspaceSection,record,kind,generation};
}
export function workspaceDestination(base: string, destination: WorkspaceDestination): string {
  const {section,record,kind,generation} = destination;
  const path = `${base}/${section}${record ? `/${section === 'memory' ? `${encodeURIComponent(kind!)}/` : ''}${encodeURIComponent(record)}` : ''}`;
  const query = new URLSearchParams();
  if (kind && !record) query.set('kind',kind);
  if (generation) query.set('generation_id',generation);
  return `${path}${query.size ? `?${query}` : ''}`;
}
export function workspaceLink(targetId: string | undefined, section: WorkspaceSection, options: {workspace?: string; owner?: string; record?: string; kind?: string; generation?: string} = {}): string {
  if (!targetId) return '/dashboard';
  const destination = {section,record:options.record,kind:options.kind,generation:options.generation};
  if (options.workspace) return workspaceDestination(workspaceBaseURL(targetId,options.workspace),destination);
  const query = new URLSearchParams({...destinationQuery(destination),...(options.owner ? {user_id:options.owner} : {})});
  return `${workspaceDirectoryURL(targetId)}?${query}`;
}
