<svelte:options runes={false} />
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { Dialog } from 'bits-ui';
  import { createAgent, fetchAgents } from '#lib/api/client.ts';
  import type { AgentRecord, MemStationTarget } from '#lib/types.ts';
  import Button from '../Button.svelte';
  import PageHeader from '../PageHeader.svelte';
  import TextInputField from '../TextInputField.svelte';
  import SelectField from '../SelectField.svelte';
  import CapabilityFields from './CapabilityFields.svelte';
  import { defaultCapabilities } from './capabilities.ts';

  export let target: MemStationTarget;
  export let userId: string;
  export let basePath = '';
  export let workspaceName = '';
  export let workspaceId = '';
  export let allowedCapabilities: string[] | undefined = undefined;
  let items: AgentRecord[] = [];
  let query = '';
  let status = '';
  let applied = { q: '', status: '' };
  let next = '';
  let loading = true;
  let error = '';
  let createError = '';
  let open = false;
  let busy = false;
  let name = '';
  let capabilities = [...defaultCapabilities];
  let request = 0;
  let alive = true;
  let controller: AbortController | undefined;
  $: base = basePath || `/users/${encodeURIComponent(userId)}/agents`;
  onMount(() => { void load(); });
  onDestroy(() => { alive = false; controller?.abort(); });

  async function load(more = false): Promise<void> {
    const version = ++request;
    controller?.abort();
    controller = new AbortController();
    loading = true;
    error = '';
    if (!more) { applied = { q: query.trim(), status }; items = []; next = ''; }
    try {
      const result = await fetchAgents(target, userId, { ...applied, after: more ? next : '' }, controller.signal);
      if (!alive || version !== request) return;
      items = more ? [...items, ...result.items.filter((item) => !items.some((old) => old.id === item.id))] : result.items;
      next = result.next_cursor;
    } catch (cause) {
      if (alive && version === request) error = cause instanceof Error ? cause.message : 'Unable to load agents.';
    } finally {
      if (alive && version === request) loading = false;
    }
  }

  async function create(): Promise<void> {
    if (busy || !name.trim()) return;
    busy = true;
    createError = '';
    try {
      const agent = await createAgent(target, userId, name.trim(), capabilities, workspaceId);
      if (!alive) return;
      open = false;
      await goto(`${base}/${encodeURIComponent(agent.id)}`);
    } catch (cause) {
      if (alive) createError = cause instanceof Error ? cause.message : 'Unable to create agent.';
    } finally { if (alive) busy = false; }
  }
</script>

<section class="page-stack">
  <PageHeader title="Agents" subtitle={workspaceName || userId}>
    <svelte:fragment slot="actions">
      <Button class="btn-primary" onclick={() => { name = ''; capabilities = defaultCapabilities.filter(cap => !allowedCapabilities || allowedCapabilities.includes(cap)); createError = ''; open = true; }}>Create agent</Button>
    </svelte:fragment>
  </PageHeader>
  <p class="panel-meta">Agents belong to this owner. Open an agent to inspect or grant its access to the selected workspace.</p>
  <form class="agent-search" on:submit|preventDefault={() => load()}>
    <TextInputField label="Search agents" bind:value={query} placeholder="Name or agent ID" maxlength={120} />
    <SelectField label="Status" bind:value={status} items={[{ value: '', label: 'All statuses' }, { value: 'active', label: 'Active' }, { value: 'disabled', label: 'Disabled' }]} />
    <Button type="submit" class="btn-secondary">Search</Button>
  </form>
  {#if error}<div class="error-state" role="alert">{error} <Button class="btn-secondary" onclick={() => load()}>Retry</Button></div>{/if}
  <div class="agent-list" aria-busy={loading}>
    {#each items as item (item.id)}
      <a class="agent-row" href={`${base}/${encodeURIComponent(item.id)}`}>
        <div><strong>{item.name}</strong><span class="helper-text mono">{item.id}</span></div>
        <span class="row-meta">{item.legacy ? 'Legacy, ' : ''}{item.status === 'active' ? 'Active' : 'Disabled'}</span>
      </a>
    {/each}
    {#if loading}<p class="panel-meta" role="status">Loading agents…</p>
    {:else if !error && items.length === 0}<p class="panel-meta">No agents match these filters.</p>{/if}
  </div>
  {#if next}<div><Button class="btn-secondary" disabled={loading} onclick={() => load(true)}>Load more agents</Button></div>{/if}
  {#if !basePath}<a class="text-link" href={`/users/${encodeURIComponent(userId)}`}>Back to user</a>{/if}
</section>

<Dialog.Root {open} onOpenChange={(value) => { if (!busy) open = value; }}>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="dialog-card agent-create-dialog" escapeKeydownBehavior={busy ? 'ignore' : 'close'} interactOutsideBehavior={busy ? 'ignore' : 'close'}>
      <Dialog.Title class="section-title">Create agent</Dialog.Title>
      <Dialog.Description class="panel-meta">Choose access to this workspace. You can issue a credential after creating the agent.</Dialog.Description>
      <form class="page-stack" on:submit|preventDefault={create}>
        <TextInputField label="Agent name" bind:value={name} maxlength={120} required disabled={busy} placeholder="e.g. Coding assistant" />
        <fieldset disabled={busy}><legend>Permissions</legend><CapabilityFields bind:value={capabilities} allowed={allowedCapabilities} disabled={busy} /></fieldset>
        {#if createError}<div class="error-state" role="alert">{createError}</div>{/if}
        <div class="dialog-actions"><Button class="btn-secondary" disabled={busy} onclick={() => open = false}>Cancel</Button><Button class="btn-primary" type="submit" disabled={busy || !name.trim()}>{busy ? 'Creating…' : 'Create agent'}</Button></div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  .agent-search { display: grid; grid-template-columns: minmax(160px, 1fr) 180px auto; gap: 12px; align-items: end; }
  .agent-row { display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 16px 4px; border-bottom: 1px solid var(--border-subtle); }
  .agent-row:hover { background: var(--surface-hover); }
  .agent-row > div { min-width: 0; overflow-wrap: anywhere; }
  .row-meta { font-size: var(--font-size-sm); color: var(--text-muted); white-space: nowrap; }
  :global(.agent-create-dialog) { display: grid; gap: 16px; width: min(560px, calc(100vw - 32px)); }
  fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
  legend { font-weight: 500; margin-bottom: 8px; }
  .dialog-actions { display: flex; gap: 8px; justify-content: flex-end; }
  @media (max-width: 600px) { .agent-search { grid-template-columns: 1fr auto; } .agent-search :global(.field:first-child) { grid-column: 1 / -1; } .agent-row { align-items: flex-start; } }
</style>
