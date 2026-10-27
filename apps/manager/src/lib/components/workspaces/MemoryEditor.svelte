<svelte:options runes={false} />
<script lang="ts">
  import MetadataRow from '#lib/components/ui/MetadataRow.svelte';
  import { onMount, onDestroy } from 'svelte';
  import { beforeNavigate, goto, invalidate } from '$app/navigation';
  import { APIError, writeWorkspaceMemory } from '#lib/api/client.ts';
  import { workspaceBaseURL, type ScopeContext } from '#lib/scope/routes.ts';
  import { useSessionDraftUnloadWarning } from '#lib/stores/session-drafts.ts';
  import type { MemoryDocument, KeyMemoryRecord } from '#lib/types.ts';
  import Button from '#lib/components/ui/Button.svelte';
  import TextareaField from '#lib/components/ui/TextareaField.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import SelectField from '#lib/components/ui/SelectField.svelte';

  export let scope: ScopeContext;
  export let record: MemoryDocument | KeyMemoryRecord | null = null;
  export let keyMemory = false;
  const creating = !record;
  const base = workspaceBaseURL(scope.target.id, scope.workspace.id);
  const storageKey = JSON.stringify(['workspace-memory-edit', scope.target.id, scope.target.baseUrl, scope.workspace.user_id, scope.workspace.id, keyMemory ? '' : (record as MemoryDocument)?.generation_id, keyMemory ? 'key_memory' : (record as MemoryDocument)?.type, record?.id ?? 'new']);
  function fields(item: typeof record) {
    return { content: item ? (keyMemory ? (item as KeyMemoryRecord).value : (item as MemoryDocument).content) : '', key: keyMemory ? (item as KeyMemoryRecord)?.key ?? '' : '', category: keyMemory ? (item as KeyMemoryRecord)?.category ?? '' : '', review: keyMemory ? (item as KeyMemoryRecord)?.review_status ?? 'pending' : 'pending' };
  }
  let current = record;
  let loaded = record;
  let draft = fields(current);
  let version = current?.updated_at ?? '';
  let editing = creating || keyMemory;
  let conflict = false;
  let busy = false;
  let error = '';
  let success = '';
  let mounted = false;
  let alive = true;
  let stored = false;
  $: dirty = JSON.stringify(draft) !== JSON.stringify(fields(current));
  $: if (mounted && record !== loaded) refresh();
  function refresh() {
    loaded = record;
    if (record?.updated_at === current?.updated_at) return;
    const preserve = dirty;
    current = record;
    if (preserve) { conflict = true; error = 'This memory changed. Your draft is preserved.'; }
    else { draft = fields(current); version = current?.updated_at ?? ''; conflict = false; error = ''; }
  }
  $: if (mounted) persist(draft, version, dirty);
  function persist(value: typeof draft, revision: string, isDirty: boolean) {
    try {
      if (isDirty) sessionStorage.setItem(storageKey, JSON.stringify({ draft: value, version: revision }));
      else sessionStorage.removeItem(storageKey);
      stored = isDirty;
    } catch { stored = false; }
  }
  useSessionDraftUnloadWarning(() => dirty && !stored);
  beforeNavigate(navigation => {
    if (busy) { navigation.cancel(); return; }
    if (!navigation.willUnload && dirty && !window.confirm(stored ? 'Keep this memory draft in this tab and switch pages?' : 'Draft storage is unavailable. Discard this draft and switch pages?')) navigation.cancel();
  });
  onMount(() => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null');
      if (saved && typeof saved.version === 'string' && saved.draft && ['content', 'key', 'category', 'review'].every(field => typeof saved.draft[field] === 'string') && saved.draft.content.length <= 131072 && saved.draft.key.length <= 256 && saved.draft.category.length <= 256) {
        draft = saved.draft; version = saved.version; editing = true;
        conflict = version !== (current?.updated_at ?? '');
        if (conflict) error = 'This draft was based on an older version. Review the current content before applying it.';
      }
    } catch { /* Keep the unload warning if storage is unavailable. */ }
    mounted = true;
  });
  onDestroy(() => { alive = false; });
  function discard() { draft = fields(current); version = current?.updated_at ?? ''; conflict = false; error = ''; success = ''; if (!keyMemory) editing = false; persist(draft, version, false); }
  async function save() {
    if (busy || conflict || !dirty || !draft.content.trim() || (creating && !draft.key.trim())) return;
    busy = true; error = ''; success = '';
    try {
      const path = keyMemory ? `keys${creating ? '' : `/${encodeURIComponent(current!.id)}`}` : `documents/${encodeURIComponent((current as MemoryDocument).type)}/${encodeURIComponent(current!.id)}`;
      const body = keyMemory ? { value: draft.content, category: draft.category, review_status: draft.review, ...(creating ? { key: draft.key } : { expected_updated_at: version }) } : { content: draft.content, expected_updated_at: version, generation_id: (current as MemoryDocument).generation_id };
      const saved = await writeWorkspaceMemory<MemoryDocument | KeyMemoryRecord>(scope.target, scope.workspace, path, creating ? 'POST' : 'PUT', body);
      if (!alive) return;
      current = saved; draft = fields(saved); version = saved.updated_at ?? ''; persist(draft, version, false);
      busy = false;
      if (creating) { await goto(`${base}/keys/${encodeURIComponent(saved.id)}`); return; }
      await invalidate('workspace:read');
      if (alive) { success = 'Memory saved.'; if (!keyMemory) editing = false; }
    } catch (cause) {
      if (alive) { conflict = !creating && cause instanceof APIError && cause.status === 409; error = creating && cause instanceof APIError && cause.status === 409 ? 'This key already exists. Choose another key or edit the existing record.' : conflict ? 'This memory changed in another session. Your draft is preserved. Load the current version before retrying.' : cause instanceof Error ? cause.message : 'Unable to save memory.'; }
    } finally { if (alive) busy = false; }
  }
  async function remove() {
    if (busy || dirty || conflict || !current || !window.confirm(`Delete key memory “${(current as KeyMemoryRecord).key}”?`)) return;
    busy = true; error = '';
    try {
      await writeWorkspaceMemory(scope.target, scope.workspace, `keys/${encodeURIComponent(current.id)}`, 'DELETE', { expected_updated_at: version });
      if (!alive) return;
      busy = false; persist(draft, version, false); await goto(`${base}/keys`, { invalidate: ['workspace:read'] });
    } catch (cause) {
      if (alive) { conflict = cause instanceof APIError && cause.status === 409; error = cause instanceof Error ? cause.message : 'Unable to delete memory.'; }
    } finally { if (alive) busy = false; }
  }
</script>

{#if !editing}
  <div class="content">{(current as MemoryDocument).content}</div>
  <Button class="btn-secondary" onclick={() => { editing = true; success = ''; }}>Edit memory</Button>
{:else}
  <form class="page-stack" on:submit|preventDefault={save}>
    {#if creating}<TextInputField label="Key" bind:value={draft.key} maxlength={256} required disabled={busy} />{/if}
    <TextareaField label={keyMemory ? 'Value' : 'Memory content'} bind:value={draft.content} rows={9} maxlength={131072} required disabled={busy} />
    {#if keyMemory}
      <TextInputField label="Category" bind:value={draft.category} maxlength={256} disabled={busy} />
      <SelectField label="Review status" bind:value={draft.review} items={[{value:'pending',label:'Pending'},{value:'approved',label:'Approved'},{value:'rejected',label:'Rejected'}]} disabled={busy} />
    {:else}<p class="helper-text">Edits apply only to the displayed generation. Source messages remain available through the source conversation link.</p>{/if}
    {#if conflict}
      <div class="notice-card page-stack">
        <p>Current saved content</p><div class="content">{fields(current).content}</div>
        {#if keyMemory}<MetadataRow items={[`Category: ${fields(current).category || 'None'}`, `Review: ${fields(current).review}`]} />{/if}
        <div class="actions"><Button class="btn-secondary" disabled={busy} onclick={() => invalidate('workspace:read')}>Load current version</Button><Button class="btn-secondary" disabled={busy || version === current?.updated_at} onclick={() => { version = current?.updated_at ?? ''; conflict = false; error = ''; }}>Use draft against this version</Button></div>
      </div>
    {/if}
    <div class="actions">
      <Button class="btn-primary" type="submit" disabled={busy || conflict || !dirty || !draft.content.trim() || (creating && !draft.key.trim())}>{busy ? 'Saving…' : creating ? 'Create key memory' : 'Save memory'}</Button>
      <Button class="btn-secondary" disabled={busy} onclick={discard}>Discard draft</Button>
      {#if keyMemory && !creating}<Button class="btn-danger" disabled={busy || dirty || conflict} onclick={remove}>Delete key memory</Button>{/if}
    </div>
    <p class="helper-text">Unsaved drafts are kept in this browser tab for this service, workspace and record. Discard removes the draft.</p>
  </form>
{/if}
{#if error}<p class="error-state" role="alert">{error}</p>{/if}
{#if success}<p role="status">{success}</p>{/if}

<style>
  form { max-width: 850px; }
  .content { white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.7; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }
</style>
