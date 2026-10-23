import { page as currentPage } from '$app/state';
import { toStore } from 'svelte/store';

// Create inside each component: page state belongs to the current SSR request.
// Legacy $: consumers need a snapshot so same-route navigation stays reactive.
export function createPageStore() {
  return toStore(() => ({ ...currentPage }));
}
