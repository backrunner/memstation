<svelte:options runes={false} />
<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import { onMount, onDestroy } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import { Dialog } from 'bits-ui';
  import { APIError, createUserAPIKey, fetchAgentCredentials, fetchAgentDetail, revokeUserAPIKey, updateAgent, updateAgentGrant } from '#lib/api/client.ts';
  import type { AgentCredential, AgentDetail, MemStationTarget } from '#lib/types.ts';
  import Button from '../Button.svelte';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import CodeBlock from '../CodeBlock.svelte';
  import ConfirmActionDialog from '../ConfirmActionDialog.svelte';
  import PageHeader from '../PageHeader.svelte';
  import SelectField from '../SelectField.svelte';
  import TextInputField from '../TextInputField.svelte';
  import CapabilityFields from './CapabilityFields.svelte';
  import { capabilitySummary } from './capabilities.ts';
  import { useSessionDraftUnloadWarning } from '#lib/stores/session-drafts.ts';

  export let target: MemStationTarget;
  export let userId: string;
  export let agentId: string;
  export let directoryPath = '';
  export let workspaceId = '';
  let detail: AgentDetail | null = null;
  let keys: AgentCredential[] = [];
  let next = '';
  let name = '';
  let caps: string[] = [];
  let loading = true;
  let keysLoading = false;
  let busy = false;
  let error = '';
  let success = '';
  let conflict = false;
  let keyError = '';
  let keyName = '';
  let keyLifetime = '30';
  let keyCaps: string[] = [];
  let keyOpen = false;
  let createdKey = '';
  let statusConfirm = false;
  let revokeCandidate: AgentCredential | null = null;
  let alive = true;
  let version = 0;
  let keyVersion = 0;
  const controller = new AbortController();
  $: nameDirty = Boolean(detail && name.trim() !== detail.agent.name);
  $: capsDirty = Boolean(detail && JSON.stringify([...caps].sort()) !== JSON.stringify([...detail.grant.capabilities].sort()));
  useSessionDraftUnloadWarning(() => nameDirty || capsDirty, 'This agent has unsaved name or permission changes. Leave this page?');
  beforeNavigate((navigation) => {
    if (!navigation.willUnload && (nameDirty || capsDirty) && !window.confirm('Discard unsaved agent changes and leave this page?')) navigation.cancel();
  });
  onMount(() => { void load(); });
  onDestroy(() => { alive = false; controller.abort(); createdKey = ''; });

  function failure(cause: unknown): void {
    error = cause instanceof Error ? cause.message : 'Unable to update agent.';
    conflict = cause instanceof APIError && cause.status === 409;
  }

  async function load(): Promise<void> {
    const request = ++version;
    loading = true;
    error = '';
    try {
      const result = await fetchAgentDetail(target, userId, agentId, controller.signal);
      if (!alive || request !== version) return;
      if (workspaceId && result.workspace.id !== workspaceId) throw new Error('This agent is not in the selected workspace.');
      detail = result;
      name = result.agent.name;
      caps = [...result.grant.capabilities];
      conflict = false;
      await loadKeys();
    } catch (cause) { if (alive && request === version) failure(cause); }
    finally { if (alive && request === version) loading = false; }
  }

  async function loadKeys(more = false): Promise<void> {
    const request = ++keyVersion;
    keysLoading = true;
    keyError = '';
    try {
      const result = await fetchAgentCredentials(target, userId, agentId, more ? next : '', controller.signal);
      if (!alive || request !== keyVersion) return;
      keys = more ? [...keys, ...result.items.filter((item) => !keys.some((old) => old.id === item.id))] : result.items;
      next = result.next_cursor;
    } catch (cause) { if (alive && request === keyVersion) keyError = cause instanceof Error ? cause.message : 'Unable to load credentials.'; }
    finally { if (alive && request === keyVersion) keysLoading = false; }
  }

  async function saveName(): Promise<void> {
    if (!detail || busy || !name.trim()) return;
    busy = true; error = ''; success = '';
    try {
      const agent = await updateAgent(target, userId, detail.agent, name.trim(), detail.agent.status);
      if (!alive) return;
      detail = { ...detail, agent }; name = agent.name;
      success = 'Agent name saved.';
    } catch (cause) { if (alive) failure(cause); }
    finally { if (alive) busy = false; }
  }

  async function saveGrant(): Promise<void> {
    if (!detail || busy) return;
    busy = true; error = ''; success = '';
    try {
      const grant = await updateAgentGrant(target, userId, detail.grant, caps);
      if (!alive) return;
      detail = { ...detail, grant, effective_capabilities: detail.agent.status === 'active' && detail.workspace.status === 'active' ? grant.capabilities.filter((cap) => detail!.workspace.capabilities.includes(cap)) : [] };
      caps = [...grant.capabilities];
      success = 'Permissions saved. Existing credential ceilings are unchanged.';
      await loadKeys();
    } catch (cause) { if (alive) failure(cause); }
    finally { if (alive) busy = false; }
  }

  async function changeStatus(): Promise<void> {
    if (!detail || busy) return;
    busy = true; error = ''; success = '';
    try {
      const agent = await updateAgent(target, userId, detail.agent, detail.agent.name, detail.agent.status === 'active' ? 'disabled' : 'active');
      if (!alive) return;
      detail = { ...detail, agent, effective_capabilities: agent.status === 'active' && detail.workspace.status === 'active' ? detail.grant.capabilities.filter((cap) => detail!.workspace.capabilities.includes(cap)) : [] };
      createdKey = '';
      statusConfirm = false;
      success = agent.status === 'disabled' ? 'Agent disabled and all its credentials revoked.' : 'Agent enabled. Issue a new credential to reconnect it.';
      await loadKeys();
    } catch (cause) { if (alive) { statusConfirm = false; failure(cause); } }
    finally { if (alive) busy = false; }
  }

  function openKey(): void {
    keyName = ''; keyLifetime = '30'; keyCaps = [...(detail?.effective_capabilities ?? [])]; keyError = ''; createdKey = ''; keyOpen = true;
  }

  async function issue(): Promise<void> {
    if (!detail || busy || !keyName.trim()) return;
    busy = true; keyError = ''; success = '';
    try {
      const result = await createUserAPIKey(target, userId, keyName.trim(), { agent_id: agentId, capabilities: keyCaps, ...(keyLifetime !== 'never' ? { expires_at: new Date(Date.now() + Number(keyLifetime) * 86400000).toISOString() } : {}) });
      if (!alive) return;
      createdKey = result.api_key;
      keyOpen = false;
      success = 'Credential created. Copy the key now; it cannot be retrieved again.';
      await loadKeys();
    } catch (cause) { if (alive) keyError = cause instanceof APIError && cause.status === 403 ? 'Access changed. Close this dialog and reload agent access before issuing a credential.' : cause instanceof Error ? cause.message : 'Unable to issue credential.'; }
    finally { if (alive) busy = false; }
  }

  async function revoke(): Promise<void> {
    if (!revokeCandidate || busy) return;
    busy = true; error = ''; success = '';
    const id = revokeCandidate.id;
    try {
      await revokeUserAPIKey(target, userId, id);
      if (!alive) return;
      revokeCandidate = null; createdKey = '';
      success = 'Credential revoked.';
      await loadKeys();
    } catch (cause) { if (alive) { revokeCandidate = null; failure(cause); } }
    finally { if (alive) busy = false; }
  }

  function keyStatus(key: AgentCredential): string {
    if (key.revoked_at) return 'Revoked';
    if (key.expires_at && new Date(key.expires_at).getTime() <= Date.now()) return 'Expired';
    return 'Active';
  }
</script>

<section class="page-stack agent-access">
  <PageHeader title={detail?.agent.name ?? 'Agent access'} subtitle={userId}>
    <svelte:fragment slot="actions"><a class="btn-secondary" href={directoryPath || `/users/${encodeURIComponent(userId)}/agents`}>All agents</a>{#if detail}<Button class="btn-primary" disabled={busy || loading || detail.agent.status !== 'active' || detail.workspace.status !== 'active'} onclick={openKey}>Issue credential</Button>{/if}</svelte:fragment>
  </PageHeader>
  {#if error}<div class="error-state" role="alert">{error}{#if conflict}<p>Your changes are still in the form. Reload the current values before saving again.</p>{/if}<Button class="btn-secondary" disabled={busy || loading} onclick={load}>{detail ? 'Discard edits and reload' : 'Retry'}</Button></div>{/if}
  {#if success}<div class="notice-card is-success" role="status">{success}</div>{/if}
  {#if createdKey}<section class="card page-stack" aria-label="New API key"><div class="page-header"><h2 class="section-title">New API key</h2><Button class="btn-secondary" onclick={() => { createdKey = ''; success = 'Credential created.'; }}>Hide key</Button></div><p class="panel-meta">Shown once. Copy and store it now.</p><CodeBlock code={createdKey} language="text" /></section>{/if}
  {#if loading}<p class="panel-meta" role="status">Loading agent access…</p>{/if}
  {#if detail}
    <p class="panel-meta">Personal workspace. {detail.agent.status === 'active' ? 'Active' : 'Disabled'}{detail.agent.legacy ? ' (legacy agent)' : ''}. Agents share this account’s Personal memory.</p>
    <div class="access-columns">
      <section class="card page-stack">
        <h2 class="section-title">Agent identity</h2>
        <form class="page-stack" on:submit|preventDefault={saveName}>
          <TextInputField label="Agent name" bind:value={name} maxlength={120} required disabled={busy || loading} />
          <div><Button type="submit" class="btn-primary" disabled={busy || loading || conflict || !nameDirty || !name.trim()}>Save name</Button></div>
        </form>
        <CollapsibleSection title="Identity details"><dl><dt>Agent ID</dt><dd class="mono">{detail.agent.id}</dd><dt>Actor ID</dt><dd class="mono">{detail.agent.actor_id}</dd><dt>Workspace</dt><dd class="mono">{detail.workspace.id}</dd></dl><Button class="btn-secondary" disabled={busy || loading} onclick={load}>Reload agent access</Button><p class="helper-text">Reloading discards unsaved name and permission changes.</p></CollapsibleSection>
        <div class="lifecycle"><p class="panel-meta">{detail.agent.status === 'active' ? 'Disabling this agent revokes every credential. Its conversations and memory remain available to the account.' : 'Enabling this agent does not restore revoked credentials.'}</p><Button class={detail.agent.status === 'active' ? 'btn-danger' : 'btn-secondary'} disabled={busy || loading || conflict} onclick={() => statusConfirm = true}>{detail.agent.status === 'active' ? 'Disable agent' : 'Enable agent'}</Button></div>
      </section>
      <section class="card page-stack">
        <h2 class="section-title">Personal permissions</h2>
        <p class="panel-meta">Credentials can only use the intersection of their ceiling, these permissions and workspace policy.</p>
        <form class="page-stack" on:submit|preventDefault={saveGrant}>
          <CapabilityFields bind:value={caps} allowed={detail.workspace.capabilities} disabled={busy || loading} />
          <p class="panel-meta">Currently effective: {capabilitySummary(detail.effective_capabilities)}.</p>
          <div><Button type="submit" class="btn-primary" disabled={busy || loading || conflict || !capsDirty}>Save permissions</Button></div>
        </form>
      </section>
    </div>
    <section class="card page-stack" aria-label="Agent credentials">
      <div class="page-header"><h2 class="section-title">Credentials</h2><div class="inline-group"><Button class="btn-secondary" disabled={keysLoading || busy} onclick={() => loadKeys()}>Refresh credentials</Button></div></div>
      {#if keyError && !keyOpen}<div class="error-state" role="alert">{keyError}</div>{/if}
      <div aria-busy={keysLoading}>
        {#each keys as key (key.id)}
          <article class="credential-row">
            <div class="credential-main"><strong>{key.name}</strong><MetadataRow items={[`${key.prefix}…`, keyStatus(key), key.expires_at ? `Expires ${new Date(key.expires_at).toLocaleString()}` : 'No expiry']} /><p class="panel-meta">Ceiling: {capabilitySummary(key.capabilities ?? [])}</p><p class="panel-meta">Effective: {capabilitySummary(keyStatus(key) === 'Active' ? key.effective_capabilities : [])}</p></div>
            {#if !key.revoked_at}<Button class="btn-secondary" disabled={busy} onclick={() => revokeCandidate = key}>Revoke</Button>{/if}
          </article>
        {:else}<p class="panel-meta">{keysLoading ? 'Loading credentials…' : 'No credentials issued for this agent.'}</p>{/each}
      </div>
      {#if next}<div><Button class="btn-secondary" disabled={keysLoading || busy} onclick={() => loadKeys(true)}>Load more credentials</Button></div>{/if}
      <p class="helper-text">Effective permissions reflect the last refresh. Every API request checks current access again.</p>
    </section>
  {/if}
</section>

<Dialog.Root open={keyOpen} onOpenChange={(value) => { if (!busy) keyOpen = value; }}>
  <Dialog.Portal><Dialog.Overlay class="dialog-overlay" /><Dialog.Content class="dialog-card credential-dialog" escapeKeydownBehavior={busy ? 'ignore' : 'close'} interactOutsideBehavior={busy ? 'ignore' : 'close'}>
    <Dialog.Title class="section-title">Issue credential</Dialog.Title><Dialog.Description class="panel-meta">For {detail?.agent.name} in Personal. The secret is shown once.</Dialog.Description>
    <form class="page-stack" on:submit|preventDefault={issue}>
      <TextInputField label="Credential name" bind:value={keyName} placeholder="e.g. Development laptop" required maxlength={120} disabled={busy} />
      <SelectField label="Lifetime" bind:value={keyLifetime} disabled={busy} items={[{ value: '7', label: '7 days' }, { value: '30', label: '30 days' }, { value: '90', label: '90 days' }, { value: 'never', label: 'No expiry' }]} />
      <fieldset disabled={busy}><legend>Permission ceiling</legend><CapabilityFields bind:value={keyCaps} allowed={detail?.effective_capabilities ?? []} disabled={busy} /></fieldset>
      {#if keyCaps.length === 0}<p class="panel-meta">This credential will have no operational access.</p>{/if}
      {#if keyError}<div class="error-state" role="alert">{keyError}</div>{/if}
      <div class="dialog-actions"><Button class="btn-secondary" disabled={busy} onclick={() => keyOpen = false}>Cancel</Button><Button type="submit" class="btn-primary" disabled={busy || !keyName.trim()}>{busy ? 'Issuing…' : 'Issue credential'}</Button></div>
    </form>
  </Dialog.Content></Dialog.Portal>
</Dialog.Root>

<ConfirmActionDialog open={statusConfirm} title={detail?.agent.status === 'active' ? 'Disable agent?' : 'Enable agent?'} description={detail?.agent.status === 'active' ? 'All credentials for this agent will be revoked immediately. Enabling it later requires issuing new credentials.' : 'The agent can receive new credentials. Previously revoked credentials stay revoked.'} subject={detail?.agent.name ?? ''} confirmLabel={detail?.agent.status === 'active' ? 'Disable agent' : 'Enable agent'} tone={detail?.agent.status === 'active' ? 'danger' : 'primary'} {busy} on:cancel={() => statusConfirm = false} on:confirm={changeStatus} />
<ConfirmActionDialog open={Boolean(revokeCandidate)} title="Revoke credential?" description="Clients using this credential will lose access immediately. This cannot be undone." subject={revokeCandidate?.name ?? ''} confirmLabel="Revoke credential" {busy} on:cancel={() => revokeCandidate = null} on:confirm={revoke} />

<style>
  .access-columns { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 20px; align-items: start; }
  .agent-access { min-width: 0; }
  .credential-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 16px 0; border-bottom: 1px solid var(--border-subtle); }
  .credential-main { min-width: 0; overflow-wrap: anywhere; }
  .credential-main p { margin: 6px 0 0; }
  .lifecycle { border-top: 1px solid var(--border-subtle); padding-top: 16px; display: grid; gap: 12px; justify-items: start; }
  dl { display: grid; gap: 6px; margin: 12px 0 0; font-size: var(--font-size-sm); }
  dt { color: var(--text-muted); } dd { margin: 0 0 8px; overflow-wrap: anywhere; }
  :global(.credential-dialog) { display: grid; gap: 16px; width: min(560px, calc(100vw - 32px)); }
  fieldset { border: 0; padding: 0; margin: 0; min-width: 0; } legend { font-weight: 500; margin-bottom: 8px; }
  .dialog-actions { display: flex; justify-content: flex-end; gap: 8px; }
  @media (max-width: 800px) { .access-columns { grid-template-columns: 1fr; } }
</style>
