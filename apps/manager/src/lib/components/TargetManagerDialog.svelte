<svelte:options runes={false} />

<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Dialog } from 'bits-ui';

  import SelectField from '$lib/components/SelectField.svelte';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { isHTTPURL } from '$lib/forms/validation';
  import type { MemStationTarget, TargetKind } from '$lib/types';
  import { parseTargetLocator } from '$lib/targets/uri';

  export let open = false;
  export let targets: MemStationTarget[] = [];
  export let selectedId = '';

  const dispatch = createEventDispatcher<{
    save: { target: Partial<MemStationTarget>; adminKey?: string };
    remove: { id: string };
  }>();

  const kindItems = [
    { value: 'single', label: 'Single' },
    { value: 'cluster-entry', label: 'Cluster Entry' },
    { value: 'node', label: 'Node' }
  ];

  const defaultBaseUrl = 'http://localhost:8080';
  const defaultEnvironment = 'custom';
  const defaultCluster = 'standalone';
  const defaultRegion = 'global';
  const defaultKind: TargetKind = 'single';

  let editingId = '';
  let label = '';
  let connectionInput = '';
  let environment = defaultEnvironment;
  let clusterName = defaultCluster;
  let region = defaultRegion;
  let kind: TargetKind = defaultKind;
  let saveError = '';
  let formErrors: Record<string, string> = {};

  $: locatorPreview = inspectLocator(connectionInput);

  $: if (open) {
    const current = targets.find((item) => item.id === selectedId) ?? targets[0];
    if (current) {
      editingId = current.id;
      label = current.label;
      connectionInput = current.baseUrl;
      environment = current.environment;
      clusterName = current.clusterName;
      region = current.region;
      kind = current.kind;
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
    editingId = target.id;
    label = target.label;
    connectionInput = target.baseUrl;
    environment = target.environment;
    clusterName = target.clusterName;
    region = target.region;
    kind = target.kind;
    saveError = '';
  }

  function resetForm(): void {
    editingId = '';
    label = '';
    connectionInput = defaultBaseUrl;
    environment = defaultEnvironment;
    clusterName = defaultCluster;
    region = defaultRegion;
    kind = defaultKind;
    saveError = '';
    formErrors = {};
  }

  function applyLocatorPreview(): void {
    if (!locatorPreview.target) {
      return;
    }
    label = locatorPreview.target.label ?? label;
    environment = locatorPreview.target.environment ?? environment;
    clusterName = locatorPreview.target.clusterName ?? clusterName;
    region = locatorPreview.target.region ?? region;
    kind = locatorPreview.target.kind ?? kind;
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
    formErrors = nextErrors;
    if (Object.keys(nextErrors).length > 0) {
      saveError = 'Please fix the highlighted target fields.';
      return;
    }

    saveError = '';
    dispatch('save', {
      target: {
        id: editingId || undefined,
        label: label.trim() || parsed.label,
        baseUrl,
        environment: environment.trim() === defaultEnvironment ? (parsed.environment ?? defaultEnvironment) : environment.trim(),
        clusterName: clusterName.trim() === defaultCluster ? (parsed.clusterName ?? defaultCluster) : clusterName.trim(),
        region: region.trim() === defaultRegion ? (parsed.region ?? defaultRegion) : region.trim(),
        kind: kind === defaultKind ? (parsed.kind ?? defaultKind) : kind
      },
      adminKey: locatorPreview.adminKey
    });
    open = false;
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="dialog-card">
      <div class="page-header">
        <Dialog.Title class="page-title" level={3}>Target Connections</Dialog.Title>
        <Dialog.Close class="button-ghost">Done</Dialog.Close>
      </div>

      <div class="page-columns">
        <section class="card page-stack">
          <div class="inline-group" style="justify-content: space-between; margin-bottom: 12px;">
            <strong>Saved Targets</strong>
            <button class="button-secondary" type="button" on:click={resetForm}>New</button>
          </div>
          {#if targets.length === 0}
            <div class="empty-state">No saved targets yet.</div>
          {:else}
            <div class="target-list">
              {#each targets as target}
                <button class={`target-row ${target.id === editingId ? 'is-active' : ''}`} type="button" on:click={() => loadTarget(target)}>
                  <div>
                    <strong>{target.label}</strong>
                    <div class="subtle-text">{target.environment} · {target.region}</div>
                  </div>
                  <span class="subtle-text">{target.kind}</span>
                </button>
              {/each}
            </div>
          {/if}
        </section>

        <section class="card page-stack">
          <div class="toolbar">
            <TextInputField
              label="Connection URI Or Base URL"
              bind:value={connectionInput}
              placeholder="memstation://127.0.0.1:8080?key=..."
              help="URI parameters can prefill label, env, cluster, region, kind, and a temporary admin key."
              error={formErrors.connection}
              fieldClass="field-span-2"
            />

            <div class="inline-group target-editor-actions">
              <button class="button-secondary" type="button" disabled={!locatorPreview.target} on:click={applyLocatorPreview}>Apply URI</button>
            </div>

            {#if locatorPreview.error}
              <div class="error-state">{locatorPreview.error}</div>
            {:else if locatorPreview.target}
              <div class="target-preview">
                <div>
                  <div class="sidebar-label">Resolved target</div>
                  <div class="target-preview-primary">{locatorPreview.target.baseUrl}</div>
                </div>
                <div class="helper-text">
                  {locatorPreview.target.label ?? 'Custom target'} · {locatorPreview.target.environment ?? defaultEnvironment}
                  {#if locatorPreview.adminKey}
                    · temporary admin key
                  {/if}
                </div>
              </div>
            {/if}

            {#if saveError}
              <div class="error-state">{saveError}</div>
            {/if}
          </div>

          <div class="form-grid two-up" style="margin-top: 16px;">
            <TextInputField label="Label" bind:value={label} placeholder="Shanghai Primary" error={formErrors.label} />
            <TextInputField label="Environment" bind:value={environment} placeholder="production" />
            <TextInputField label="Cluster" bind:value={clusterName} placeholder="cluster-a" />
            <TextInputField label="Region" bind:value={region} placeholder="ap-east-1" />
            <SelectField label="Kind" bind:value={kind} items={kindItems} />
          </div>

          <div class="toolbar" style="margin-top: 16px;">
            <div class="helper-text">`key=` stays in memory for this tab and is never written to browser storage.</div>
            <div class="inline-group" style="justify-content: flex-end;">
              {#if editingId}
                <button class="button-danger" type="button" on:click={() => dispatch('remove', { id: editingId })}>Remove</button>
              {/if}
              <button class="button" type="button" on:click={saveTarget}>Save Target</button>
            </div>
          </div>
        </section>
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  .target-preview {
    display: grid;
    gap: 6px;
    padding: 14px 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
  }

  .target-preview-primary {
    font-weight: 600;
  }

  .target-list {
    display: grid;
    gap: 10px;
  }

  .target-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 14px 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: rgba(255, 255, 255, 0.82);
    color: var(--text-primary);
    text-align: left;
  }

  .target-row.is-active {
    border-color: rgba(15, 94, 168, 0.22);
    background: rgba(18, 94, 168, 0.08);
  }

  .target-editor-actions {
    justify-content: flex-end;
  }
</style>
