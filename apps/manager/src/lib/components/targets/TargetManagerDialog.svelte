<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte';
  import { AlertDialog, ContextMenu, Dialog, DropdownMenu } from 'bits-ui';
  import { Ellipsis, Trash2 } from '@lucide/svelte';

  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import { isHTTPURL } from '#lib/forms/validation.ts';
  import type { MemStationTarget } from '#lib/types.ts';
  import { availableTargets } from '#lib/stores/targets.ts';
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import { parseTargetLocator } from '#lib/targets/uri.ts';

  export let open = false;
  export let targets: MemStationTarget[] = [];
  export let selectedId = '';

  export let onsave: (input: { target: Partial<MemStationTarget>; adminKey?: string }) => Promise<void>;
  export let onremove: (id: string) => Promise<void>;
  let saving = false;
  let removing = false;
  let wasOpen = false;

  const defaultBaseUrl = 'http://localhost:8080';
  const defaultEnvironment = 'custom';
  const defaultCluster = 'standalone';
  const defaultRegion = 'global';
  const draftTargetId = '__new-target-draft__';

  let editingId = '';
  let label = '';
  let connectionInput = '';
  let adminKey = '';
  let saveError = '';
  let formErrors: Record<string, string> = {};
  let removeConfirmOpen = false;
  let removeError = '';
  $: pending = saving || removing;
  let removeTargetCandidate: MemStationTarget | undefined;

  $: locatorPreview = inspectLocator(connectionInput);
  $: resolvedAdminKey = locatorPreview.adminKey ?? adminKey.trim();
  $: draftTargetRow =
    editingId === draftTargetId
      ? buildDraftTargetRow(label, connectionInput, locatorPreview)
      : undefined;
  $: targetRows = draftTargetRow ? [draftTargetRow, ...targets] : targets;

  $: if (open !== wasOpen) {
    wasOpen = open;
    if (open) {
      const current = targets.find((item) => item.id === selectedId) ?? targets[0];
      if (current) loadTarget(current); else resetForm();
    } else { adminKey = ''; connectionInput = ''; }
  }

  function inspectLocator(input: string): { target?: Partial<MemStationTarget>; adminKey?: string; error?: string } {
    if (!input.trim()) {
      return {};
    }
    try {
      const parsed = parseTargetLocator(input);
      return parsed;
    } catch (error) {
      return {
        error: error instanceof Error ? error.message : 'Invalid connection URI'
      };
    }
  }

  function loadTarget(target: MemStationTarget): void {
    if (pending || isDraftTarget(target)) {
      return;
    }
    editingId = target.id;
    label = target.label;
    connectionInput = target.baseUrl;
    adminKey = '';
    formErrors = {};
    saveError = '';
  }

  function resetForm(): void {
    if (pending) return;
    editingId = draftTargetId;
    label = '';
    connectionInput = $availableTargets[0]?.baseUrl ?? defaultBaseUrl;
    adminKey = '';
    saveError = '';
    formErrors = {};
  }

  async function saveTarget(): Promise<void> {
    if (pending) return;
    const nextErrors: Record<string, string> = {};
    if (locatorPreview.error) {
      saveError = locatorPreview.error;
      return;
    }

    const parsed = locatorPreview.target ?? {};
    const baseUrl = parsed.baseUrl ?? connectionInput.trim();
    if (!baseUrl) {
      nextErrors.connection = 'Enter a target base URL or connection URI.';
    } else if (!isHTTPURL(baseUrl)) {
      nextErrors.connection = 'Target URL must resolve to http:// or https://.';
    }
    if (!label.trim() && !parsed.label?.trim()) {
      nextErrors.label = 'Give this target a short label so it is easy to recognize.';
    }
    if ((editingId === draftTargetId || targets.find((item) => item.id === editingId)?.baseUrl !== baseUrl) && !resolvedAdminKey) {
      nextErrors.adminKey = 'Admin token is required to connect a new target.';
    }
    formErrors = nextErrors;
    if (Object.keys(nextErrors).length > 0) {
      saveError = 'Please fix the highlighted target fields.';
      return;
    }

    saveError = '';
    const isDraft = editingId === draftTargetId;
    saving = true;
    try {
    await onsave({
      target: {
        id: isDraft ? undefined : editingId || undefined,
        label: label.trim() || parsed.label,
        baseUrl,
        environment: defaultEnvironment,
        clusterName: defaultCluster,
        region: defaultRegion,
        kind: 'single'
      },
      adminKey: resolvedAdminKey || undefined
    });
    open = false;
    } catch (cause) {
      saveError = cause instanceof Error ? cause.message : 'Connection failed';
    } finally {
      saving = false;
      adminKey = '';
      connectionInput = baseUrl;
    }
  }

  function isDraftTarget(target: MemStationTarget): boolean {
    return target.id === draftTargetId;
  }

  function buildDraftTargetRow(
    draftLabel: string,
    draftConnectionInput: string,
    preview: { target?: Partial<MemStationTarget> }
  ): MemStationTarget {
    const hasDraftInput = Boolean(
      draftLabel.trim() || draftConnectionInput.trim() !== ($availableTargets[0]?.baseUrl ?? defaultBaseUrl)
    );
    const parsed = hasDraftInput ? (preview.target ?? {}) : {};
    const rawConnection = draftConnectionInput.trim();
    return {
      id: draftTargetId,
      label: draftLabel.trim() || parsed.label?.trim() || 'New target',
      baseUrl: parsed.baseUrl ?? (rawConnection || defaultBaseUrl),
      environment: defaultEnvironment,
      clusterName: defaultCluster,
      region: defaultRegion,
      kind: 'single'
    };
  }

  function requestRemoveTarget(target: MemStationTarget): void {
    if (pending) return;
    removeError = '';
    removeTargetCandidate = target;
    removeConfirmOpen = true;
  }

  function cancelRemoveTarget(): void {
    removeTargetCandidate = undefined;
    removeConfirmOpen = false;
  }

  async function confirmRemoveTarget(): Promise<void> {
    if (pending || !removeTargetCandidate) {
      return;
    }
    removing = true;
    try {
      await onremove(removeTargetCandidate.id);
      removeTargetCandidate = undefined;
      removeConfirmOpen = false;
      removing = false;
      const current = targets[0];
      if (current) loadTarget(current); else resetForm();
    } catch (cause) { removeError = cause instanceof Error ? cause.message : 'Could not remove target'; }
    finally { removing = false; }
  }
</script>

<Dialog.Root {open} onOpenChange={(value) => { if (!saving && !removing) open = value; }}>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="dialog-card">
      <div class="page-header target-dialog-header">
        <div class="page-header-meta">
          <Dialog.Title class="page-title" level={3}>Target connections</Dialog.Title>
          <p class="panel-meta">Connect to services registered by your Manager administrator.</p>
        </div>
        <Dialog.Close class="btn-ghost btn-sm" disabled={saving || removing}>Close</Dialog.Close>
      </div>

      <div class="target-dialog-body">
        <section class="target-dialog-list">
          <div class="target-dialog-list-header">
            <span class="sidebar-label">Saved targets</span>
            <Button class="btn-secondary btn-sm" type="button" onclick={resetForm} disabled={pending}>New target</Button>
          </div>
          {#if targetRows.length === 0}
            <div class="empty-state">No saved targets yet. Add one to connect.</div>
          {:else}
            <div class="target-list">
              {#each targetRows as target}
                {#if isDraftTarget(target)}
                  <Button class="target-row is-active is-draft" type="button">
                    <div class="target-row-primary">
                      <strong>{target.label}</strong>
                      <span class="helper-text">{target.baseUrl}</span>
                    </div>
                    <span class="status-badge warning">draft</span>
                  </Button>
                {:else}
                  <div class="target-list-entry">
                  <ContextMenu.Root>
                    <ContextMenu.Trigger class="target-context-trigger">
                      <Button
                        class="target-row {target.id === editingId ? 'is-active' : ''}"

                        type="button"
                        disabled={pending}
                        onclick={() => loadTarget(target)}
                      >
                        <div class="target-row-primary">
                          <strong>{target.label}</strong>
                          <span class="helper-text">{target.baseUrl}</span>
                        </div>
                      </Button>
                    </ContextMenu.Trigger>
                    <ContextMenu.Portal>
                      <ContextMenu.Content class="select-content target-context-menu" sideOffset={6}>
                        <ContextMenu.Item
                          class="select-item target-context-menu-danger"
                          onSelect={() => requestRemoveTarget(target)}
                        >
                          <Trash2 size={13} strokeWidth={1.85} />
                          <span>Delete target</span>
                        </ContextMenu.Item>
                      </ContextMenu.Content>
                    </ContextMenu.Portal>
                  </ContextMenu.Root>
                  <DropdownMenu.Root>
                    <DropdownMenu.Trigger class="btn-ghost btn-icon btn-sm" disabled={pending} aria-label={`Actions for ${target.label}`}><Ellipsis size={16} /></DropdownMenu.Trigger>
                    <DropdownMenu.Portal>
                      <DropdownMenu.Content class="select-content target-context-menu" sideOffset={6} align="end">
                        <DropdownMenu.Item class="select-item target-context-menu-danger" onSelect={() => requestRemoveTarget(target)}>
                          <Trash2 size={13} strokeWidth={1.85} /><span>Delete target</span>
                        </DropdownMenu.Item>
                      </DropdownMenu.Content>
                    </DropdownMenu.Portal>
                  </DropdownMenu.Root>
                  </div>
                {/if}
              {/each}
            </div>
          {/if}
        </section>

        <section class="target-dialog-form">
          {#if $availableTargets.length > 1}
            <SelectField label="Available service" value={$availableTargets.find((item) => item.baseUrl === locatorPreview.target?.baseUrl)?.id ?? ''}
              items={$availableTargets.map((item) => ({ value: item.id, label: item.label }))} disabled={pending}
              onValueChange={(id) => { const target = $availableTargets.find((item) => item.id === id); if (target) { connectionInput = target.baseUrl; label = target.label; adminKey = ''; } }} />
          {/if}
          <div class="field">
            <TextInputField
              label="Connection URI or base URL"
              bind:value={connectionInput}
              disabled={pending}
              placeholder="memstation://127.0.0.1:8080?key=..."
              help="URI parameters can prefill label and key=. New targets exchange the token once for a session."
              error={locatorPreview.error ?? formErrors.connection}
            />
          </div>

          <TextInputField label="Label" bind:value={label} disabled={pending} placeholder={locatorPreview.target?.label ?? 'Shanghai Primary'} error={formErrors.label} />

          {#if !locatorPreview.adminKey}
            <TextInputField
              label="Admin token"
              bind:value={adminKey}
              disabled={pending}
              type="password"
              autocomplete="off"
              placeholder="MEMSTATION_ADMIN_TOKEN"
              help="Required when adding a new target unless the URI includes key=. The token is used once; reconnecting requires entering it again."
              error={formErrors.adminKey}
              inputClass="input-mono"
            />
          {/if}

          {#if saveError}
            <div class="error-state">{saveError}</div>
          {/if}

          <div class="target-dialog-footer">
            <p class="helper-text">
              Labels are saved on this browser. Login sessions are held by Manager for each service.
            </p>
            <div class="inline-group">
              <Button class="btn-primary" type="button" onclick={saveTarget} disabled={saving || removing}>{saving ? 'Connecting…' : 'Save target'}</Button>
            </div>
          </div>
        </section>
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<AlertDialog.Root open={removeConfirmOpen} onOpenChange={(value) => { if (!removing) removeConfirmOpen = value; }}>
  <AlertDialog.Portal>
    <AlertDialog.Overlay class="dialog-overlay target-remove-overlay" />
    <AlertDialog.Content class="dialog-card target-remove-dialog">
      <div class="target-remove-copy">
        <AlertDialog.Title class="section-title" level={3}>Delete target?</AlertDialog.Title>
        <AlertDialog.Description class="panel-meta">
          Remove {removeTargetCandidate?.label ?? 'this target'} from this manager. The MemStation server and its data are not changed.
        </AlertDialog.Description>
      </div>
      <div class="target-remove-summary">
        <strong>{removeTargetCandidate?.label ?? 'Target'}</strong>
        <span class="helper-text mono">{removeTargetCandidate?.baseUrl ?? ''}</span>
      </div>
      {#if removeError}<div class="error-state" role="alert">{removeError}</div>{/if}
      <div class="target-remove-actions">
        <Button class="btn-secondary" type="button" onclick={cancelRemoveTarget} disabled={removing}>Cancel</Button>
        <Button class="btn-danger" type="button" onclick={confirmRemoveTarget} disabled={removing}>{removing ? 'Removing…' : 'Delete target'}</Button>
      </div>
    </AlertDialog.Content>
  </AlertDialog.Portal>
</AlertDialog.Root>

<style>
  .target-dialog-header { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: start; }
  .target-list-entry { display: grid; grid-template-columns: minmax(0, 1fr) 30px; align-items: center; }
  .target-dialog-body {
    display: grid;
    grid-template-columns: 220px minmax(0, 1fr);
    gap: 16px;
    align-items: start;
  }

  .target-dialog-list {
    display: grid;
    gap: 8px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: var(--surface);
    overflow: hidden;
  }

  .target-dialog-list-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    min-height: 40px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface-muted);
  }

  .target-dialog-form {
    display: grid;
    gap: 12px;
  }

  .target-list {
    display: grid;
    gap: 1px;
    max-height: 320px;
    overflow-y: auto;
    padding: 0;
    background: var(--border-subtle);
  }

  :global(.target-context-trigger) {
    display: block;
    min-width: 0;
  }

  :global(.target-row) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    width: 100%;
    min-height: 44px;
    padding: 7px 10px;
    border: 0;
    border-radius: 0;
    background: var(--surface);
    color: var(--text);
    text-align: left;
    cursor: pointer;
    transition: border-color 120ms ease, background-color 120ms ease;
  }

  :global(.target-row:hover) {
    background: var(--surface-hover);
  }

  :global(.target-row.is-active) {
    background: var(--brand-softer);
    box-shadow: inset 3px 0 0 var(--brand);
  }

  :global(.target-row.is-draft) {
    background: var(--warning-soft);
  }

  :global(.target-context-menu) {
    min-width: 170px;
  }

  :global(.target-context-menu-danger) {
    gap: 8px;
    color: var(--danger);
  }

  :global(.target-context-menu-danger[data-highlighted]),
  :global(.target-context-menu-danger:hover) {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .target-row-primary {
    display: grid;
    gap: 1px;
    min-width: 0;
  }

  .target-row-primary strong {
    overflow: hidden;
    color: var(--text);
    font-size: var(--font-size-sm);
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  :global(.target-row.is-active) .target-row-primary strong {
    color: var(--text);
  }

  .target-row-primary .helper-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-mono);
    font-size: 11px;
    max-width: 160px;
  }

  .target-dialog-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding-top: 12px;
    margin-top: 4px;
    border-top: 1px solid var(--border);
  }

  .target-dialog-footer .helper-text {
    max-width: 320px;
  }


  :global(.target-remove-overlay) {
    z-index: 110;
  }

  :global(.target-remove-dialog) {
    z-index: 120;
    max-width: 420px;
  }

  .target-remove-copy {
    display: grid;
    gap: 4px;
  }

  .target-remove-summary {
    display: grid;
    gap: 4px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
  }

  .target-remove-summary .helper-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .target-remove-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }

  @media (max-width: 720px) {
    .target-dialog-body {
      grid-template-columns: 1fr;
    }
    .target-dialog-footer {
      flex-direction: column;
      align-items: stretch;
    }
  }
</style>
