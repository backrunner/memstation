import { browser } from '$app/env';
import { derived, get, writable } from 'svelte/store';

import { configureTargetCatalog, registeredTarget } from '#lib/targets/catalog.ts';
import type { AdminSessionState, MemStationTarget } from '#lib/types.ts';

const TARGETS_STORAGE_KEY = 'memstation.manager.targets';
const SELECTED_STORAGE_KEY = 'memstation.manager.selected-target';
let sessionCheckGeneration = 0;

interface TargetState {
  loaded: boolean;
  targets: MemStationTarget[];
  selectedId: string;
  session: AdminSessionState;
}

function normalizeBaseUrl(value: string): string {
  return value.trim().replace(/\/$/, '');
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

export const availableTargets = writable<MemStationTarget[]>([]);
export const targetState = writable<TargetState>({ loaded: false, targets: [], selectedId: '', session: { authenticated: false, checking: false } });

export const selectedTarget = derived(targetState, ($state) => {
  return $state.targets.find((item) => item.id === $state.selectedId) ?? $state.targets[0] ?? emptyTarget();
});

export function initializeTargets(catalog: MemStationTarget[]): void {
  sessionCheckGeneration++;
  if (!browser) return;
  configureTargetCatalog(catalog);
  availableTargets.set(catalog);
  let saved: Partial<MemStationTarget>[] = catalog.slice(0, 1);
  let selected = '';
  try {
    const raw = window.localStorage.getItem(TARGETS_STORAGE_KEY);
    if (raw !== null) { const parsed: unknown = JSON.parse(raw); saved = Array.isArray(parsed) ? parsed : []; }
    selected = window.localStorage.getItem(SELECTED_STORAGE_KEY) ?? '';
  } catch { /* Storage is optional; the server registry remains authoritative. */ }
  const targets: MemStationTarget[] = [];
  let selectedId = '';
  for (const item of saved) {
    if (!item || typeof item.baseUrl !== 'string') continue;
    const registered = catalog.find((target) => target.baseUrl === normalizeBaseUrl(item.baseUrl!));
    if (!registered) continue;
    if (item.id === selected) selectedId = registered.id;
    if (!targets.some((target) => target.id === registered.id)) targets.push({ ...registered, label: typeof item.label === 'string' ? item.label.slice(0, 120) : registered.label });
  }
  selectedId ||= targets[0]?.id ?? '';
  const state = { loaded: true, targets, selectedId, session: sessionForTarget(selectedId) };
  targetState.set(state);
  persistState(state);
}

function persistState(state: TargetState): void {
  if (!browser) {
    return;
  }
  try {
    window.localStorage.setItem(TARGETS_STORAGE_KEY, JSON.stringify(state.targets));
    window.localStorage.setItem(SELECTED_STORAGE_KEY, state.selectedId);
  } catch { /* Preferences are optional. */ }
}

export function selectTarget(targetId: string): void {
  sessionCheckGeneration++;
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
  sessionCheckGeneration++;
  let savedTarget = emptyTarget();
  targetState.update((state) => {
    const current = input.id ? state.targets.find((item) => item.id === input.id) : undefined;
    const registered = registeredTarget({ baseUrl: input.baseUrl ?? current?.baseUrl ?? '' });
    const nextTarget: MemStationTarget = { ...registered, label: input.label?.trim() || current?.label || registered.label };


    const targets = [nextTarget, ...state.targets.filter((item) => item.id !== nextTarget.id && item.id !== current?.id)];

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
  sessionCheckGeneration++;
  targetState.update((state) => {
    const targets = state.targets.filter((item) => item.id !== targetId);
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
  sessionCheckGeneration++;
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
  sessionCheckGeneration++;
  targetState.update((state) => ({ ...state, session }));
}

export function markTargetSessionConnected(targetId: string, checkedAt = new Date().toISOString()): void {
  sessionCheckGeneration++;
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

export function targetSessionGeneration(): number { return sessionCheckGeneration; }

export function markTargetSessionDisconnected(targetId: string, expectedGeneration?: number): void {
  if (expectedGeneration !== undefined && expectedGeneration !== sessionCheckGeneration) return;
  if (get(targetState).selectedId !== targetId) return;
  sessionCheckGeneration++;
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

export function beginTargetSessionCheck(targetId: string): number {
  const generation = ++sessionCheckGeneration;
  targetState.update((state) => state.selectedId === targetId ? { ...state, session: { authenticated: false, checking: true } } : state);
  return generation;
}
export function finishTargetSessionCheck(targetId: string, generation: number, session: AdminSessionState): void {
  targetState.update((state) => state.selectedId === targetId && generation === sessionCheckGeneration ? { ...state, session } : state);
}
