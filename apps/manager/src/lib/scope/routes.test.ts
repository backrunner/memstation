import { describe, expect, it } from 'vitest';
import { switchWorkspaceURL, workspaceMemoryURL } from './routes.ts';

describe('workspace switching', () => {
  it('opens related Key Memory through its generation-independent editor', () => {
    expect(workspaceMemoryURL('/t/service/w/personal', 'key_memory', 'key/one', 'old-generation')).toBe('/t/service/w/personal/keys/key%2Fone');
    expect(workspaceMemoryURL('/t/service/w/personal', 'fact', 'fact-one', 'archive')).toBe('/t/service/w/personal/memory/fact/fact-one?generation_id=archive');
  });
  it('drops record identity, generation and cursor while retaining section filters', () => {
    expect(switchWorkspaceURL(new URL('https://manager/t/a/w/old/memory/fact/id?q=tea&kind=fact&generation_id=old&after=cursor&user_id=owner&actor_id=speaker'), 'a', 'new')).toBe('/t/a/w/new/memory?q=tea&kind=fact');
    expect(switchWorkspaceURL(new URL('https://manager/t/a/w/old/keys/id?generation_id=old&q=tea'), 'a', 'new')).toBe('/t/a/w/new/keys?q=tea');
  });
  it('keeps historical search criteria across workspace changes', () => {
    const path = switchWorkspaceURL(new URL('https://manager/t/a/w/old/memory/search?q=tea&known_at=2026-01-01T00%3A00%3A00Z&generation_id=old'), 'a', 'new');
    expect(path).toBe('/t/a/w/new/memory/search?q=tea&known_at=2026-01-01T00%3A00%3A00Z');
  });
});
