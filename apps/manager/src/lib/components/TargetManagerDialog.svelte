<svelte:options runes={false} />

<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { AlertDialog, ContextMenu, Dialog } from 'bits-ui';
  import { Trash2 } from 'lucide-svelte';

  import TextInputField from '$lib/components/TextInputField.svelte';
  import { isHTTPURL } from '$lib/forms/validation';
  import type { MemStationTarget } from '$lib/types';
  import { parseTargetLocator } from '$lib/targets/uri';

  export let open = false;
  export let targets: MemStationTarget[] = [];
  export let selectedId = '';

  const dispatch = createEventDispatcher<{
    save: { target: Partial<MemStationTarget>; adminKey?: string };
    remove: { id: string };
  }>();

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
  let removeTargetCandidate: MemStationTarget | undefined;

  $: locatorPreview = inspectLocator(connectionInput);
  $: resolvedAdminKey = locatorPreview.adminKey ?? adminKey.trim();
  $: draftTargetRow =
    editingId === draftTargetId
      ? buildDraftTargetRow(label, connectionInput, locatorPreview)
      : undefined;
  $: targetRows = draftTargetRow ? [draftTargetRow, ...targets] : targets;

  $: if (open) {
    const current = targets.find((item) => item.id === selectedId) ?? targets[0];
    if (current) {
      editingId = current.id;
      label = current.label;
      connectionInput = current.baseUrl;
      adminKey = '';
      saveError = '';
      formErrors = {};
    }
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
    if (isDraftTarget(target)) {
      return;
    }
    editingId = target.id;
    label = target.label;
    connectionInput = target.baseUrl;
    saveError = '';
  }

  function resetForm(): void {
    editingId = draftTargetId;
    label = '';
    connectionInput = defaultBaseUrl;
    adminKey = '';
    saveError = '';
    formErrors = {};
  }

  function saveTarget(): void {
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
    if (editingId === draftTargetId && !resolvedAdminKey) {
      nextErrors.adminKey = 'Admin token is required to connect a new target.';
    }
    formErrors = nextErrors;
    if (Object.keys(nextErrors).length > 0) {
      saveError = 'Please fix the highlighted target fields.';
      return;
    }

    saveError = '';
    const isDraft = editingId === draftTargetId;
    dispatch('save', {
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
      draftLabel.trim() || draftConnectionInput.trim() !== defaultBaseUrl
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
    removeTargetCandidate = target;
    removeConfirmOpen = true;
  }

  function cancelRemoveTarget(): void {
    removeTargetCandidate = undefined;
    removeConfirmOpen = false;
  }

  function confirmRemoveTarget(): void {
    if (!removeTargetCandidate) {
      return;
    }
    dispatch('remove', { id: removeTargetCandidate.id });
    removeTargetCandidate = undefined;
    removeConfirmOpen = false;
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="dialog-card">
      <div class="page-header">
        <div class="page-header-meta">
          <Dialog.Title class="page-title" level={3}>Target connections</Dialog.Title>
          <p class="panel-meta">Manage MemStation endpoints this manager can connect to.</p>
        </div>
        <Dialog.Close class="btn-ghost btn-sm">Close</Dialog.Close>
      </div>

      <div class="target-dialog-body">
        <section class="target-dialog-list">
          <div class="target-dialog-list-header">
            <span class="sidebar-label">Saved targets</span>
            <button class="btn-secondary btn-sm" type="button" on:click={resetForm}>New target</button>
          </div>
          {#if targetRows.length === 0}
            <div class="empty-state">No saved targets yet. Add one to connect.</div>
          {:else}
            <div class="target-list">
              {#each targetRows as target}
                {#if isDraftTarget(target)}
                  <button class="target-row is-active is-draft" type="button">
                    <div class="target-row-primary">
                      <strong>{target.label}</strong>
                      <span class="helper-text">{target.baseUrl}</span>
                    </div>
                    <span class="status-badge warning">draft</span>
                  </button>
                {:else}
                  <ContextMenu.Root>
                    <ContextMenu.Trigger class="target-context-trigger">
                      <button
                        class="target-row"
                        class:is-active={target.id === editingId}
                        type="button"
                        on:click={() => loadTarget(target)}
                      >
                        <div class="target-row-primary">
                          <strong>{target.label}</strong>
                          <span class="helper-text">{target.baseUrl}</span>
                        </div>
                      </button>
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
                {/if}
              {/each}
            </div>
          {/if}
        </section>

        <section class="target-dialog-form">
          <div class="field">
            <TextInputField
              label="Connection URI or base URL"
              bind:value={connectionInput}
              placeholder="memstation://127.0.0.1:8080?key=..."
              help="URI parameters can prefill label and key=. New targets exchange the token once for a session."
              error={locatorPreview.error ?? formErrors.connection}
            />
          </div>

          <TextInputField label="Label" bind:value={label} placeholder={locatorPreview.target?.label ?? 'Shanghai Primary'} error={formErrors.label} />

          {#if !locatorPreview.adminKey}
            <TextInputField
              label="Admin token"
              bind:value={adminKey}
              type="password"
              autocomplete="off"
              placeholder="MEMSTATION_ADMIN_TOKEN"
              help="Required when adding a new target unless the URI includes key=. The token is not saved in browser storage."
              error={formErrors.adminKey}
              inputClass="input-mono"
            />
          {/if}

          {#if saveError}
            <div class="error-state">{saveError}</div>
          {/if}

          <div class="target-dialog-footer">
            <p class="helper-text">
              Target metadata is saved locally; admin tokens are only exchanged for HttpOnly sessions.
            </p>
            <div class="inline-group">
              <button class="btn-primary" type="button" on:click={saveTarget}>Save target</button>
            </div>
          </div>
        </section>
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<AlertDialog.Root bind:open={removeConfirmOpen}>
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
      <div class="target-remove-actions">
        <button class="btn-secondary" type="button" on:click={cancelRemoveTarget}>Cancel</button>
        <button class="btn-danger" type="button" on:click={confirmRemoveTarget}>Delete target</button>
      </div>
    </AlertDialog.Content>
  </AlertDialog.Portal>
</AlertDialog.Root>

<style>
  .target-dialog-body {
    display: grid;
    grid-template-columns: 260px minmax(0, 1fr);
    gap: 20px;
    align-items: start;
  }

  .target-dialog-list {
    display: grid;
    gap: 10px;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: var(--surface-muted);
  }

  .target-dialog-list-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .target-dialog-form {
    display: grid;
    gap: 12px;
  }

  .target-list {
    display: grid;
    gap: 6px;
    max-height: 320px;
    overflow-y: auto;
  }

  :global(.target-context-trigger) {
    display: block;
    min-width: 0;
  }

  .target-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    width: 100%;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
    color: var(--text);
    text-align: left;
    cursor: pointer;
    transition: border-color 120ms ease, background-color 120ms ease;
  }

  .target-row:hover {
    border-color: var(--border-strong);
    background: var(--surface-hover);
  }

  .target-row.is-active {
    border-color: var(--brand);
    background: var(--brand-soft);
  }

  .target-row.is-draft {
    border-style: dashed;
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
    gap: 2px;
    min-width: 0;
  }

  .target-row-primary strong {
    font-size: var(--font-size-md);
    color: var(--text);
  }

  .target-row-primary .helper-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-mono);
    font-size: 11px;
    max-width: 180px;
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
