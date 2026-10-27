import { describe, expect, it } from 'vitest';
import { readWorkspaceDestination, workspaceDestination, workspaceLink } from './navigation.ts';
describe('current workspace navigation',()=>{
  it('keeps explicit record identity and generation',()=>{
    expect(workspaceDestination('/t/service/w/group',{section:'memory',kind:'fact',record:'fact/one',generation:'archive'})).toBe('/t/service/w/group/memory/fact/fact%2Fone?generation_id=archive');
    expect(workspaceLink('service','keys',{workspace:'group',record:'key-1'})).toBe('/t/service/w/group/keys/key-1');
  });
  it('requires a workspace selection from global pages',()=>{
    const url = new URL(workspaceLink('service','conversations',{owner:'alice',record:'thread'}),'http://manager.test');
    expect(url.pathname).toBe('/t/service/workspaces');
    expect(url.searchParams.get('user_id')).toBe('alice');
    expect(readWorkspaceDestination(url.searchParams)).toEqual({section:'conversations',record:'thread',kind:undefined,generation:undefined});
  });
  it('rejects obsolete navigation and malformed destinations',()=>{
    for (const query of ['open=/memory','manager_target=service','section=memory&record=x','section=keys&generation=archive','section=conversations&kind=fact','section=memory&kind=facts','section=memory&section=keys','section=https://evil.test']) expect(()=>readWorkspaceDestination(new URLSearchParams(query)),query).toThrow();
  });
});
