<svelte:options runes={false} />
<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchAdminMemoryDocuments, fetchEntityAliases, putEntityAlias, type EntityAlias } from '$lib/api/client';
  import type { MemStationTarget, MemoryDocument } from '$lib/types';
  import TextInputField from './TextInputField.svelte';
  import SelectField from './SelectField.svelte';
  import ConfirmActionDialog from './ConfirmActionDialog.svelte';

  export let target: MemStationTarget;
  export let document: MemoryDocument;
  let aliases: EntityAlias[] = [];
  let entities: MemoryDocument[] = [];
  let alias = '';
  let scope = '';
  let sourceNode = '';
  let reason = '';
  let error = '';
  let busy = false;
  let mounted = false;
  let loadedKey = '';
  let revision = 0;
  let confirmation = false;
  let pending: EntityAlias | null = null;
  $: key = `${target.id}:${document.user_id}:${document.generation_id}:${document.node_id}`;
  $: visible = aliases.filter((item) => item.target_node_id === document.node_id || item.source_node_id === document.node_id);
  $: choices = [{ value: '', label: 'Name alias only' }, ...entities.filter((item) => item.node_id !== document.node_id).map((item) => ({ value: item.node_id, label: String(item.metadata?.title ?? item.content) }))];
  onMount(() => { mounted = true; return () => { revision++; }; });
  $: if (mounted && key !== loadedKey) { loadedKey = key; alias = ''; scope = String(document.metadata?.entity_scope ?? ''); sourceNode = ''; reason = ''; confirmation = false; void load(); }
  async function load(): Promise<void> {
    const current = ++revision;
    error = '';
    try {
      const [records, nodes] = await Promise.all([fetchEntityAliases(target, document.user_id, document.generation_id), fetchAdminMemoryDocuments(target, { kind: 'entity', userId: document.user_id, limit: 200 })]);
      if (current !== revision) return;
      aliases = records;
      entities = nodes.filter((item) => item.generation_id === document.generation_id);
    } catch (cause) { if (current === revision) error = cause instanceof Error ? cause.message : 'Could not load entity aliases'; }
  }
  async function save(item: Omit<EntityAlias, 'id'>): Promise<void> {
    if (busy) return;
    const currentKey = key;
    busy = true; error = '';
    try {
      await putEntityAlias(target, item);
      if (key !== currentKey) return;
      confirmation = false;
      await load();
    } catch (cause) { if (key === currentKey) error = cause instanceof Error ? cause.message : 'Could not save entity alias'; }
    finally { busy = false; }
  }
  function review(item: EntityAlias, status: 'confirmed' | 'revoked'): void { pending = { ...item, status }; confirmation = true; }
</script>
<section class="page-stack">
  <h3 class="section-title">Entity names and merges</h3>
  <p class="helper-text">Confirmed mappings connect matching names and source entities in this scope. Revoking a mapping preserves both original records and their evidence.</p>
  {#if error}<div class="error-state" role="alert">{error}</div>{/if}
  <form class="page-stack" on:submit|preventDefault={() => save({ user_id: document.user_id, generation_id: document.generation_id ?? '', alias, scope, source_node_id: sourceNode, target_node_id: document.node_id, status: 'candidate', reason })}>
    <div class="filters two-up">
      <TextInputField label="Alias" bind:value={alias} placeholder="Another name for this entity" />
      <TextInputField label="Scope" bind:value={scope} placeholder="Project or workspace" />
      <SelectField label="Source entity" bind:value={sourceNode} items={choices} help="Optionally connect another entity's memories after review." />
      <TextInputField label="Mapping reason" bind:value={reason} />
    </div>
    <div><button class="btn-secondary" type="submit" disabled={busy || !alias.trim() || !reason.trim()}>Save candidate</button></div>
  </form>
  <div class="data-list">
    {#each visible as item (item.id)}
      <div class="data-list-row">
        <strong>{item.alias}</strong> <span class="helper-text">{item.scope || 'Default scope'} · {item.status}</span>
        <p>{item.reason}</p>
        <div class="toolbar">
          {#if item.status !== 'confirmed'}<button class="btn-secondary btn-sm" disabled={busy} on:click={() => review(item, 'confirmed')}>Confirm mapping</button>{/if}
          {#if item.status !== 'revoked'}<button class="btn-ghost btn-sm" disabled={busy} on:click={() => review(item, 'revoked')}>Revoke mapping</button>{/if}
        </div>
      </div>
    {/each}
  </div>
</section>
<ConfirmActionDialog bind:open={confirmation} title={pending?.status === 'revoked' ? 'Revoke entity mapping' : 'Confirm entity mapping'} description="This changes which entity memories can be recalled together in the selected scope." subject={pending?.alias ?? ''} confirmLabel={pending?.status === 'revoked' ? 'Revoke' : 'Confirm'} {busy} tone="primary" on:confirm={() => pending && save(pending)} />
