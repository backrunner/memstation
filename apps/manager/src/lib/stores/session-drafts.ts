import { browser } from '$app/environment';
import { beforeNavigate } from '$app/navigation';
import { onMount } from 'svelte';

const SESSION_DRAFT_PREFIX = 'memstation.manager.session-draft';
const draftCache = new Map<string, string>();

type StoredSessionDraft<T> = {
  version: number;
  value: T;
};

export function buildSessionDraftKey(scope: string, targetId?: string | null, suffix?: string | number | null): string {
  return [SESSION_DRAFT_PREFIX, scope, targetId ?? 'global', suffix ?? 'default'].join(':');
}

export function readSessionDraft<T>(key: string, version: number): T | null {
  if (!browser) {
    return null;
  }

  const raw = window.sessionStorage.getItem(key);
  if (!raw) {
    draftCache.delete(key);
    return null;
  }

  draftCache.set(key, raw);

  try {
    const parsed = JSON.parse(raw) as StoredSessionDraft<T>;
    if (parsed.version !== version) {
      clearSessionDraft(key);
      return null;
    }
    return parsed.value;
  } catch {
    clearSessionDraft(key);
    return null;
  }
}

export function writeSessionDraft<T>(key: string, version: number, value: T): void {
  if (!browser) {
    return;
  }

  try {
    const raw = JSON.stringify({ version, value } satisfies StoredSessionDraft<T>);
    if (draftCache.get(key) === raw) {
      return;
    }
    window.sessionStorage.setItem(key, raw);
    draftCache.set(key, raw);
  } catch {
    // Keep draft persistence best-effort; UI editing should not fail on storage quota or serialization errors.
  }
}

export function clearSessionDraft(key: string): void {
  if (!browser) {
    return;
  }

  window.sessionStorage.removeItem(key);
  draftCache.delete(key);
}

export function draftsEqual(left: unknown, right: unknown): boolean {
  try {
    return JSON.stringify(left) === JSON.stringify(right);
  } catch {
    return false;
  }
}

export function useSessionDraftUnloadWarning(
  isDirty: () => boolean,
  message = 'Draft changes in this tab will be lost if you close or reload the page.'
): void {
  beforeNavigate((navigation) => {
    if (!browser || !navigation.willUnload || !isDirty()) {
      return;
    }
    navigation.cancel();
  });

  onMount(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent): string | undefined => {
      if (!isDirty()) {
        return undefined;
      }
      event.preventDefault();
      event.returnValue = message;
      return message;
    };

    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => {
      window.removeEventListener('beforeunload', handleBeforeUnload);
    };
  });
}

export function useSessionDraftPersistence(persist: () => void): void {
  beforeNavigate(() => {
    if (!browser) {
      return;
    }
    persist();
  });

  onMount(() => {
    const flushDraft = (): void => {
      persist();
    };

    window.addEventListener('pagehide', flushDraft);
    window.addEventListener('beforeunload', flushDraft);
    return () => {
      window.removeEventListener('pagehide', flushDraft);
      window.removeEventListener('beforeunload', flushDraft);
    };
  });
}
