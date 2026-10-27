<svelte:options runes={false} />
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { beforeNavigate, invalidate } from '$app/navigation';
  import { APIError, updateWorkspace } from '#lib/api/client.ts';
  import { workspaceDirectoryURL, workspaceDraftKey, type ScopeContext } from '#lib/scope/routes.ts';
  import { useSessionDraftUnloadWarning } from '#lib/stores/session-drafts.ts';
  import Button from '#lib/components/ui/Button.svelte';
  import CollapsibleSection from '#lib/components/ui/CollapsibleSection.svelte';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import CapabilityFields from '../agents/CapabilityFields.svelte';
  import { capabilityOptions, capabilitySummary } from '../agents/capabilities.ts';

  export let scope: ScopeContext;
  let workspace = scope.workspace;
  let loadedWorkspace = scope.workspace;
  let draftPolicyVersion = workspace.policy_version;
  let name = workspace.name;
  let capabilities = [...workspace.capabilities];
  let error = '';
  let success = '';
  let conflict = false;
  let busy = false;
  let mounted = false;
  let alive = true;
  let draftStored = false;
  const key = workspaceDraftKey(scope);
  $: dirty = name.trim() !== workspace.name || JSON.stringify([...capabilities].sort()) !== JSON.stringify([...workspace.capabilities].sort());
  $: if (mounted && scope.workspace !== loadedWorkspace) refreshWorkspace();
  function refreshWorkspace(): void {
    loadedWorkspace = scope.workspace;
    if (scope.workspace.policy_version === workspace.policy_version) return;
    const preserveDraft = dirty;
    workspace = scope.workspace;
    if (preserveDraft) {
      conflict = true; error = 'The saved workspace has changed. Review the loaded policy before applying your draft.';
    } else {
      name = workspace.name; capabilities = [...workspace.capabilities]; draftPolicyVersion = workspace.policy_version;
      conflict = false; error = '';
    }
  }
  $: if (mounted) persist(name, capabilities, dirty);
  useSessionDraftUnloadWarning(() => dirty && !draftStored);
  beforeNavigate((navigation) => {
    if (busy) { navigation.cancel(); return; }
    if (!navigation.willUnload && dirty && !window.confirm(draftStored
      ? 'Keep this workspace draft in this tab and switch pages? Cancel to continue editing.'
      : 'This draft could not be saved in this tab. Discard it and switch pages?')) navigation.cancel();
  });
  onMount(() => {
    try {
      const draft = JSON.parse(sessionStorage.getItem(key) ?? 'null');
      if (draft && typeof draft.name === 'string' && draft.name.length <= 240 && Array.isArray(draft.capabilities) && draft.capabilities.every((cap: unknown) => capabilityOptions.some((item) => item.value === cap)) && Number.isSafeInteger(draft.policyVersion)) {
        name = draft.name; capabilities = draft.capabilities; draftPolicyVersion = draft.policyVersion;
        if (draft.policyVersion !== workspace.policy_version) { conflict = true; error = 'This draft was based on an older workspace version. Review the current policy before applying it.'; }
      }
    } catch { /* Storage may be unavailable. Keep the unload warning active. */ }
    mounted = true;
  });
  onDestroy(() => { alive = false; });

  function persist(draftName: string, draftCapabilities: string[], isDirty: boolean): void {
    try {
      if (isDirty) sessionStorage.setItem(key, JSON.stringify({ name: draftName, capabilities: draftCapabilities, policyVersion: draftPolicyVersion }));
      else sessionStorage.removeItem(key);
      draftStored = isDirty;
    } catch { draftStored = false; }
  }
  async function save(): Promise<void> {
    if (busy || conflict || !dirty || !name.trim()) return;
    busy = true; error = ''; success = '';
    try {
      const saved = await updateWorkspace(scope.target, workspace, name.trim(), capabilities);
      if (!alive) return;
      workspace = saved; draftPolicyVersion = saved.policy_version; name = saved.name; capabilities = [...saved.capabilities];
      persist(name, capabilities, false);
      await invalidate('workspace:detail');
      if (alive) success = 'Workspace saved. Agent permissions now use this policy; credential ceilings and grants are unchanged.';
    } catch (cause) {
      if (alive) {
        conflict = cause instanceof APIError && cause.status === 409;
        error = conflict ? 'This workspace changed in another session. Your draft is preserved. Load the current version to review it.' : cause instanceof Error ? cause.message : 'Unable to save workspace.';
      }
    } finally { if (alive) busy = false; }
  }
  function discard(): void {
    name = workspace.name; capabilities = [...workspace.capabilities]; draftPolicyVersion = workspace.policy_version; conflict = false; error = ''; success = '';
    persist(name, capabilities, false);
  }
</script>

<section class="page-stack workspace-settings">
  <PageHeader title={workspace.name} subtitle={`Owned by ${workspace.user_id}`} />
  <a class="text-link" href={workspaceDirectoryURL(scope.target.id)}>All workspaces</a>
  <form class="page-stack" on:submit|preventDefault={save}>
    <TextInputField label="Workspace name" bind:value={name} maxlength={120} required disabled={busy || workspace.status !== 'active'} />
    <fieldset disabled={busy || workspace.status !== 'active'}>
      <legend>Workspace permissions</legend>
      <p class="panel-meta">These permissions limit every agent in this workspace. Re-enabling a permission restores access only where both the agent grant and credential allow it. Admin access is unchanged.</p>
      <CapabilityFields bind:value={capabilities} disabled={busy || workspace.status !== 'active'} />
    </fieldset>
    {#if conflict}
      <div class="notice-card">
        <p>Loaded policy: {capabilitySummary(workspace.capabilities)}</p>
        <div class="workspace-actions">
          <Button class="btn-secondary" onclick={() => invalidate('workspace:detail')}>Load current version</Button>
          <Button class="btn-secondary" disabled={draftPolicyVersion === workspace.policy_version} onclick={() => { draftPolicyVersion = workspace.policy_version; conflict = false; error = ''; persist(name, capabilities, dirty); }}>Use draft against this version</Button>
        </div>
        <p class="helper-text">Review the saved policy before retrying. A newer change will still cause a conflict.</p>
      </div>
    {/if}
    {#if error}<p class="error-state" role="alert">{error}</p>{/if}
    {#if success}<p class="panel-meta" role="status">{success}</p>{/if}
    <div class="workspace-actions">
      <Button class="btn-primary" type="submit" disabled={busy || conflict || !dirty || !name.trim() || workspace.status !== 'active'}>{busy ? 'Saving…' : 'Save workspace'}</Button>
      {#if dirty}<Button class="btn-secondary" disabled={busy} onclick={discard}>Discard draft</Button>{/if}
    </div>
  </form>
  <CollapsibleSection title="Workspace details">
    <dl><dt>Workspace ID</dt><dd class="mono">{workspace.id}</dd><dt>Policy version</dt><dd>{workspace.policy_version}</dd><dt>Updated</dt><dd>{workspace.updated_at}</dd><dt>Status</dt><dd>{workspace.status}</dd></dl>
  </CollapsibleSection>
</section>

<style>
  .workspace-settings { max-width: 820px; }
  fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
  legend { font-weight: 600; margin-bottom: 8px; }
  .workspace-actions { display: flex; gap: 8px; flex-wrap: wrap; }
  dl { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 20px; }
  dd { margin: 0; overflow-wrap: anywhere; }
  dt { color: var(--text-muted); }
</style>
