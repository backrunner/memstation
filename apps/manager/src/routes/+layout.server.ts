import { error } from '@sveltejs/kit';
import { publicTarget, targetRegistry } from '#lib/server/targets/registry.ts';
import { ensureManagerSession } from '#lib/server/targets/sessions.ts';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async ({ cookies, url, setHeaders }) => {
  setHeaders({ 'cache-control': 'no-store' });
  let availableTargets;
  try {
    availableTargets = targetRegistry(process.env).map(publicTarget);
    await ensureManagerSession(cookies, url);
  } catch { error(503, 'Manager connections are unavailable. Check the target and session-store configuration.'); }
  if (url.searchParams.has('manager_target')) error(400, 'Unsupported navigation parameter. Choose a service and workspace.');
  return { availableTargets };
};
