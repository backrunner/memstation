import { error, redirect } from '@sveltejs/kit';
import { workspaceRequest } from '#lib/server/workspaces.ts';
import { workspaceBaseURL, workspaceDirectoryURL } from '#lib/scope/routes.ts';
import { workspaceDestination } from '#lib/scope/navigation.ts';
import type { WorkspacePage } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ parent, params, url, fetch }) => {
  await parent();
  const href = `${url.pathname.slice(url.pathname.indexOf('/open/') + 5)}${url.search}`;
  if (!workspaceDestination('', href)) error(400, 'This record link cannot be mapped to a workspace.');
  const owner = url.searchParams.get('user_id') ?? '';
  const result = await workspaceRequest<WorkspacePage>(fetch, params.target, `?${new URLSearchParams({ user_id: owner, limit: '2' })}`);
  if (!result.value) return { failure: result.failure };
  if (result.value.workspace_mode !== 'personal_only') error(400, 'Select a supported Personal workspace.');
  const items = result.value.items.filter(item => item.kind === 'personal' && (!owner || item.user_id === owner));
  if (items.length === 1 && !result.value.next_cursor) redirect(307, workspaceDestination(workspaceBaseURL(params.target, items[0].id), href)!);
  if (owner) error(404, 'No Personal workspace is available for this owner.');
  redirect(307, `${workspaceDirectoryURL(params.target)}?${new URLSearchParams({ open: href })}`);
};
