import { error } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async ({ parent, params }) => {
  const { availableTargets } = await parent();
  const routeTarget = availableTargets.find((target) => target.id === params.target);
  if (!routeTarget) error(404, 'This service is not registered in Manager.');
  return { routeTarget };
};
