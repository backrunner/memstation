import { workspaceRequest } from '#lib/server/workspaces.ts';
import type { WorkspaceRecord } from '#lib/types.ts';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async ({ parent, params, fetch, depends }) => {
  await parent();
  depends('workspace:detail');
  return { workspaceResult: await workspaceRequest<WorkspaceRecord>(fetch, params.target, `/${encodeURIComponent(params.workspace)}`) };
};
