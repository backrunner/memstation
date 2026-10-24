import { describe, expect, it } from 'vitest';
import { legacyReviewURL, workspaceDestination, workspaceEntryURL } from './navigation.ts';

describe('workspace navigation compatibility', () => {
  it('retains record identity and historical generation in explicit workspace routes', () => {
    expect(workspaceDestination('/t/service/w/personal', '/memory/facts/fact%2Fone?user_id=owner&generation_id=archive')).toBe('/t/service/w/personal/memory/fact/fact%2Fone?generation_id=archive');
    expect(workspaceDestination('/t/service/w/personal', '/memory/items?type=agent_action&user_id=owner')).toBe('/t/service/w/personal/memory?kind=agent_action');
    expect(workspaceDestination('/t/service/w/personal', '/memory/keys/key-1?user_id=owner')).toBe('/t/service/w/personal/keys/key-1');
    expect(workspaceDestination('/t/service/w/personal', '/memory?q=tea&known_at=2026-01-01T00%3A00%3A00Z')).toBe('/t/service/w/personal/memory/search?q=tea&known_at=2026-01-01T00%3A00%3A00Z');
  });
  it('requires owner resolution for unscoped entry links and never guesses admin', () => {
    expect(workspaceEntryURL('service', '/conversations?user_id=alice')).toBe('/t/service/open/conversations?user_id=alice');
    expect(workspaceEntryURL('service', '/memory/keys', 'alice-space')).toBe('/t/service/w/alice-space/keys');
    const review = new URL(legacyReviewURL('service', '/memory/keys/key-1', 'alice'), 'http://manager.test');
    expect(review.searchParams.get('manager_target')).toBe('service');
    expect(review.searchParams.has('target_id')).toBe(false);
    expect(review.searchParams.get('user_id')).toBe('alice');
  });
  it('rejects foreign scope hints, stale cursors, duplicate owners and redirects', () => {
    for (const href of ['https://evil.test/memory', '//evil.test/memory', '/memory/../../login', '/memory?user_id=a&user_id=b', '/memory?workspace_id=foreign', '/conversations?after=old', '/memory/keys?generation_id=archive', '/settings/system']) expect(workspaceDestination('/t/service/w/personal', href), href).toBeNull();
  });
});
