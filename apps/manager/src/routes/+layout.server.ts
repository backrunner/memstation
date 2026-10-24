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
  const legacyTargetId = url.searchParams.get('manager_target');
  if (url.searchParams.getAll('manager_target').length > 1) error(400, 'Choose one service for this record.');
  if (legacyTargetId && !availableTargets.some(target => target.id === legacyTargetId)) error(404, 'This service is not registered in Manager.');
  return { availableTargets, legacyTargetId };
};
