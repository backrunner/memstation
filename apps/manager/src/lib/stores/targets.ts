import { browser } from '$app/environment';
import { derived, get, writable } from 'svelte/store';

import type { AdminSessionState, MemStationTarget } from '$lib/types';

const TARGETS_STORAGE_KEY = 'memstation.manager.targets';
const SELECTED_STORAGE_KEY = 'memstation.manager.selected-target';

interface TargetState {
  loaded: boolean;
  targets: MemStationTarget[];
  selectedId: string;
  session: AdminSessionState;
}

function normalizeBaseUrl(value: string): string {
  return value.trim().replace(/\/$/, '');
}

function defaultTarget(): MemStationTarget {
  const baseUrl = normalizeBaseUrl(import.meta.env.PUBLIC_MEMSTATION_DEFAULT_TARGET ?? 'http://localhost:8080');
  return {
    id: 'local-default',
    label: 'Local Dev',
    baseUrl,
    environment: 'local',
    clusterName: 'standalone',
    region: 'local',
    kind: 'single'
  };
}

function initialState(): TargetState {
  if (browser) {
    return restoreStateFromStorage();
  }
  const target = defaultTarget();
  return {
    loaded: false,
    targets: [target],
    selectedId: target.id,
    session: { authenticated: false, checking: false }
  };
}

export const targetState = writable<TargetState>(initialState());

export const selectedTarget = derived(targetState, ($state) => {
  return $state.targets.find((item) => item.id === $state.selectedId) ?? $state.targets[0] ?? defaultTarget();
});

function restoreStateFromStorage(): TargetState {
  const fallback = defaultTarget();
  const rawTargets = window.localStorage.getItem(TARGETS_STORAGE_KEY);
  const rawSelected = window.localStorage.getItem(SELECTED_STORAGE_KEY);

  let targets = [fallback];
  if (rawTargets) {
    try {
      const parsed = JSON.parse(rawTargets) as MemStationTarget[];
      if (Array.isArray(parsed) && parsed.length > 0) {
        targets = parsed.map((item) => ({ ...item, baseUrl: normalizeBaseUrl(item.baseUrl) }));
      }
    } catch (error) {
      console.warn('failed to restore saved targets', error);
    }
  }

  const selectedId = rawSelected && targets.some((item) => item.id === rawSelected) ? rawSelected : targets[0]?.id ?? fallback.id;
  return {
    loaded: true,
    targets,
    selectedId,
    session: { authenticated: false, checking: false }
  };
}

export function initializeTargets(): void {
  if (!browser) {
    return;
  }
  targetState.set(restoreStateFromStorage());
}

function persistState(state: TargetState): void {
  if (!browser) {
    return;
  }
  window.localStorage.setItem(TARGETS_STORAGE_KEY, JSON.stringify(state.targets));
  window.localStorage.setItem(SELECTED_STORAGE_KEY, state.selectedId);
}

export function selectTarget(targetId: string): void {
  targetState.update((state) => {
    const next = {
      ...state,
      selectedId: state.targets.some((item) => item.id === targetId) ? targetId : state.selectedId,
      session: { authenticated: false, checking: true }
    };
    persistState(next);
    return next;
  });
}

export function upsertTarget(input: Partial<MemStationTarget>): void {
  targetState.update((state) => {
    const current = input.id ? state.targets.find((item) => item.id === input.id) : undefined;
    const nextTarget: MemStationTarget = {
      id: input.id ?? crypto.randomUUID(),
      label: input.label?.trim() || current?.label || 'Custom Target',
      baseUrl: normalizeBaseUrl(input.baseUrl ?? current?.baseUrl ?? defaultTarget().baseUrl),
      environment: input.environment?.trim() || current?.environment || 'custom',
      clusterName: input.clusterName?.trim() || current?.clusterName || 'standalone',
      region: input.region?.trim() || current?.region || 'global',
      kind: input.kind ?? current?.kind ?? 'single'
    };

    const targets = current
      ? state.targets.map((item) => (item.id === nextTarget.id ? nextTarget : item))
      : [nextTarget, ...state.targets.filter((item) => item.baseUrl !== nextTarget.baseUrl)];

    const next = {
      ...state,
      targets,
      selectedId: nextTarget.id
    };
    persistState(next);
    return next;
  });
}

export function removeTarget(targetId: string): void {
  targetState.update((state) => {
    const fallback = defaultTarget();
    let targets = state.targets.filter((item) => item.id !== targetId);
    if (targets.length === 0) {
      targets = [fallback];
    }
    const selectedId = targets.some((item) => item.id === state.selectedId) ? state.selectedId : targets[0].id;
    const next = {
      ...state,
      targets,
      selectedId
    };
    persistState(next);
    return next;
  });
}

export function updateSession(session: AdminSessionState): void {
  targetState.update((state) => ({ ...state, session }));
}

export function currentTarget(): MemStationTarget {
  return get(selectedTarget);
}
