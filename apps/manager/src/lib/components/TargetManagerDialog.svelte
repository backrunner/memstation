<svelte:options runes={false} />

<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Dialog, Select } from 'bits-ui';

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
    if (locatorPreview.error) {
      saveError = locatorPreview.error;
      return;
    }

    const parsed = locatorPreview.target ?? {};
    const baseUrl = parsed.baseUrl ?? connectionInput.trim();
    if (!baseUrl) {
      saveError = 'Enter a target base URL or connection URI.';
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
        <div>
          <Dialog.Title class="page-title" level={3}>Targets</Dialog.Title>
          <Dialog.Description class="page-description">Paste a base URL or the URI printed by `memstation serve`.</Dialog.Description>
        </div>
        <Dialog.Close class="button-ghost">Done</Dialog.Close>
      </div>

      <div class="grid-2">
        <section class="card">
          <div class="inline-group" style="justify-content: space-between; margin-bottom: 12px;">
            <strong>Saved Targets</strong>
            <button class="button-secondary" type="button" on:click={resetForm}>New</button>
          </div>
          <div class="toolbar">
            {#each targets as target}
              <button class={`nav-link ${target.id === editingId ? 'is-active' : ''}`} type="button" on:click={() => loadTarget(target)}>
                <span>{target.label}</span>
                <span class="subtle-text">{target.environment}</span>
              </button>
            {/each}
          </div>
        </section>

        <section class="card">
          <div class="toolbar">
            <label class="field">
              <span class="field-label">Connection URI or Base URL</span>
              <div class="inline-group" style="align-items: stretch;">
                <input class="input" bind:value={connectionInput} placeholder="memstation://127.0.0.1:8080?key=..." />
                <button class="button-secondary" type="button" disabled={!locatorPreview.target} on:click={applyLocatorPreview}>Apply URI</button>
              </div>
            </label>

            {#if locatorPreview.error}
              <div class="error-state">{locatorPreview.error}</div>
            {:else if locatorPreview.target}
              <div class="target-preview">
                <div>
                  <div class="sidebar-label">Resolved target</div>
                  <div class="target-preview-primary">{locatorPreview.target.baseUrl}</div>
                </div>
                <div class="target-preview-meta">
                  <span>{locatorPreview.target.label ?? 'Custom target'}</span>
                  <span>{locatorPreview.target.environment ?? defaultEnvironment}</span>
                  {#if locatorPreview.adminKey}
                    <span>temporary admin key</span>
                  {/if}
                </div>
              </div>
            {/if}

            {#if saveError}
              <div class="error-state">{saveError}</div>
            {/if}
          </div>

          <div class="form-grid two-up" style="margin-top: 16px;">
            <label class="field">
              <span class="field-label">Label</span>
              <input class="input" bind:value={label} placeholder="Shanghai Primary" />
            </label>
            <label class="field">
              <span class="field-label">Environment</span>
              <input class="input" bind:value={environment} placeholder="production" />
            </label>
            <label class="field">
              <span class="field-label">Cluster</span>
              <input class="input" bind:value={clusterName} placeholder="cluster-a" />
            </label>
            <label class="field">
              <span class="field-label">Region</span>
              <input class="input" bind:value={region} placeholder="ap-east-1" />
            </label>
            <div class="field">
              <span class="field-label">Kind</span>
              <Select.Root type="single" bind:value={kind} items={kindItems}>
                <Select.Trigger class="select-trigger">{kindItems.find((item) => item.value === kind)?.label ?? 'Select kind'}</Select.Trigger>
                <Select.Portal>
                  <Select.Content class="select-content" sideOffset={8}>
                    <Select.Viewport>
                      {#each kindItems as item}
                        <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                      {/each}
                    </Select.Viewport>
                  </Select.Content>
                </Select.Portal>
              </Select.Root>
            </div>
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
    gap: 10px;
    padding: 14px 16px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: var(--bg-panel-muted);
  }

  .target-preview-primary {
    font-weight: 600;
  }

  .target-preview-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    color: var(--text-secondary);
    font-size: 0.9rem;
  }

  .target-preview-meta span {
    padding: 4px 10px;
    border-radius: 999px;
    background: rgba(15, 94, 168, 0.08);
  }
</style>
