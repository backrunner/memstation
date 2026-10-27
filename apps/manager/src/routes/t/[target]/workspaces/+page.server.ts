import { error } from '@sveltejs/kit';
import { readWorkspaceDestination, destinationQuery } from '#lib/scope/navigation.ts';
import { workspaceRequest } from '#lib/server/workspaces.ts';
import type { WorkspacePage } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ parent, params, url, fetch, depends }) => {
  await parent();
  depends('workspace:directory');
  let destination;
  try { destination = readWorkspaceDestination(url.searchParams); } catch { error(400,'Invalid workspace destination.'); }
  const filters = { q: url.searchParams.get('q') ?? '', user_id: url.searchParams.get('user_id') ?? '', after: url.searchParams.get('after') ?? '' };
  const result = await workspaceRequest<WorkspacePage>(fetch, params.target, `?${new URLSearchParams({ ...filters, limit: '50' })}`);
  return { directory: result, filters, destination, destinationFields:destination ? destinationQuery(destination) : {} };
};
