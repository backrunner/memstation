import { browser } from '$app/env';
import { PUBLIC_MEMSTATION_DEFAULT_TARGET } from '$app/env/public';
import { derived, get, writable } from 'svelte/store';

import { parseTargetLocator } from '#lib/targets/uri.ts';
import type { AdminSessionState, MemStationTarget } from '#lib/types.ts';

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

function stageInitialTarget(input: string | undefined): Partial<MemStationTarget> | null {
  const value = input?.trim();
  if (!value) {
    return null;
  }

  try {
    const parsed = parseTargetLocator(value);
    return parsed.target;
  } catch (error) {
    console.warn('failed to parse PUBLIC_MEMSTATION_DEFAULT_TARGET', error);
    return null;
  }
}

function disconnectedSession(targetId?: string): AdminSessionState {
  return {
    authenticated: false,
    checking: false,
    checkedAt: new Date().toISOString(),
    error: targetId ? 'Connect this target.' : undefined
  };
}

function sessionForTarget(targetId?: string): AdminSessionState {
  if (!targetId) {
    return { authenticated: false, checking: false };
  }
  return { authenticated: false, checking: true };
}

function defaultTarget(): MemStationTarget {
  const staged = stageInitialTarget(PUBLIC_MEMSTATION_DEFAULT_TARGET);
  if (!staged) {
    return emptyTarget();
  }
  const baseUrl = normalizeBaseUrl(staged?.baseUrl ?? 'http://localhost:8080');
  return {
    id: 'local-default',
    label: staged?.label?.trim() || 'Local Dev',
    baseUrl,
    environment: 'custom',
    clusterName: 'standalone',
    region: 'global',
    kind: 'single'
  };
}

function emptyTarget(): MemStationTarget {
  return {
    id: '',
    label: '',
    baseUrl: '',
    environment: '',
    clusterName: '',
    region: '',
    kind: 'single'
  };
}

function initialState(): TargetState {
  if (browser) {
    return restoreStateFromStorage();
  }
  const target = defaultTarget();
  const targets = target.id ? [target] : [];
  return {
    loaded: false,
    targets,
    selectedId: target.id,
    session: sessionForTarget(target.id)
  };
}

export const targetState = writable<TargetState>(initialState());

export const selectedTarget = derived(targetState, ($state) => {
  return $state.targets.find((item) => item.id === $state.selectedId) ?? $state.targets[0] ?? emptyTarget();
});

function restoreStateFromStorage(): TargetState {
  const fallback = defaultTarget();
  const rawTargets = window.localStorage.getItem(TARGETS_STORAGE_KEY);
  const rawSelected = window.localStorage.getItem(SELECTED_STORAGE_KEY);

  let targets = fallback.id ? [fallback] : [];
  if (rawTargets) {
    try {
      const parsed = JSON.parse(rawTargets) as MemStationTarget[];
      if (Array.isArray(parsed) && parsed.length > 0) {
        targets = parsed
          .filter((item) => item.id && item.baseUrl)
          .map((item) => ({ ...item, baseUrl: normalizeBaseUrl(item.baseUrl) }));
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
    session: sessionForTarget(selectedId)
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
    const selectedId = state.targets.some((item) => item.id === targetId) ? targetId : state.selectedId;
    const next = {
      ...state,
      selectedId,
      session: sessionForTarget(selectedId)
    };
    persistState(next);
    return next;
  });
}

export function upsertTarget(input: Partial<MemStationTarget>): MemStationTarget {
  let savedTarget = defaultTarget();
  targetState.update((state) => {
    const current = input.id ? state.targets.find((item) => item.id === input.id) : undefined;
    const nextTarget: MemStationTarget = {
      id: input.id ?? crypto.randomUUID(),
      label: input.label?.trim() || current?.label || 'Custom Target',
      baseUrl: normalizeBaseUrl(input.baseUrl ?? current?.baseUrl ?? defaultTarget().baseUrl),
      environment: 'custom',
      clusterName: 'standalone',
      region: 'global',
      kind: 'single'
    };

    const targets = current
      ? state.targets.map((item) => (item.id === nextTarget.id ? nextTarget : item))
      : [nextTarget, ...state.targets.filter((item) => item.baseUrl !== nextTarget.baseUrl)];

    const next = {
      ...state,
      targets,
      selectedId: nextTarget.id,
      session: sessionForTarget(nextTarget.id)
    };
    persistState(next);
    savedTarget = nextTarget;
    return next;
  });
  return savedTarget;
}

export function removeTarget(targetId: string): void {
  targetState.update((state) => {
    const fallback = defaultTarget();
    let targets = state.targets.filter((item) => item.id !== targetId);
    if (targets.length === 0 && fallback.id) {
      targets = [fallback];
    }
    const selectedId = targets.some((item) => item.id === state.selectedId) ? state.selectedId : (targets[0]?.id ?? '');
    const next = {
      ...state,
      targets,
      selectedId,
      session: sessionForTarget(selectedId)
    };
    persistState(next);
    return next;
  });
}

export function clearAllTargets(): void {
  const existing = get(targetState);
  const next = {
    ...existing,
    targets: [],
    selectedId: '',
    session: { authenticated: false, checking: false }
  };
  targetState.set(next);
  persistState(next);
}

export function updateSession(session: AdminSessionState): void {
  targetState.update((state) => ({ ...state, session }));
}

export function markTargetSessionConnected(targetId: string, checkedAt = new Date().toISOString()): void {
  targetState.update((state) => {
    if (state.selectedId !== targetId) {
      return state;
    }
    return {
      ...state,
      session: { authenticated: true, checking: false, checkedAt }
    };
  });
}

export function markTargetSessionDisconnected(targetId: string): void {
  targetState.update((state) => {
    if (state.selectedId !== targetId) {
      return state;
    }
    return {
      ...state,
      session: disconnectedSession(targetId)
    };
  });
}

export function currentTarget(): MemStationTarget {
  return get(selectedTarget);
}
