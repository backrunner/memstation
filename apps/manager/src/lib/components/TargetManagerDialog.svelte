<svelte:options runes={false} />

<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Dialog, Select } from 'bits-ui';

  import type { MemStationTarget, TargetKind } from '$lib/types';

  export let open = false;
  export let targets: MemStationTarget[] = [];
  export let selectedId = '';

  const dispatch = createEventDispatcher<{
    save: Partial<MemStationTarget>;
    remove: { id: string };
  }>();

  const kindItems = [
    { value: 'single', label: 'Single' },
    { value: 'cluster-entry', label: 'Cluster Entry' },
    { value: 'node', label: 'Node' }
  ];

  let editingId = '';
  let label = '';
  let baseUrl = '';
  let environment = 'custom';
  let clusterName = 'standalone';
  let region = 'global';
  let kind: TargetKind = 'single';

  $: if (open) {
    const current = targets.find((item) => item.id === selectedId) ?? targets[0];
    if (current) {
      editingId = current.id;
      label = current.label;
      baseUrl = current.baseUrl;
      environment = current.environment;
      clusterName = current.clusterName;
      region = current.region;
      kind = current.kind;
    }
  }

  function loadTarget(target: MemStationTarget): void {
    editingId = target.id;
    label = target.label;
    baseUrl = target.baseUrl;
    environment = target.environment;
    clusterName = target.clusterName;
    region = target.region;
    kind = target.kind;
  }

  function resetForm(): void {
    editingId = '';
    label = '';
    baseUrl = 'http://localhost:8080';
    environment = 'custom';
    clusterName = 'standalone';
    region = 'global';
    kind = 'single';
  }

  function saveTarget(): void {
    dispatch('save', {
      id: editingId || undefined,
      label,
      baseUrl,
      environment,
      clusterName,
      region,
      kind
    });
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="dialog-card">
      <div class="page-header">
        <div>
          <Dialog.Title class="page-title" level={3}>Manage Targets</Dialog.Title>
          <Dialog.Description class="page-description">
            Save named MemStation backends and switch between local, staging, production, or future cluster entry points.
          </Dialog.Description>
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
          <div class="form-grid two-up">
            <label class="field">
              <span class="field-label">Label</span>
              <input class="input" bind:value={label} placeholder="Shanghai Primary" />
            </label>
            <label class="field">
              <span class="field-label">Base URL</span>
              <input class="input" bind:value={baseUrl} placeholder="https://memstation.example.com" />
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

          <div class="inline-group" style="justify-content: space-between; margin-top: 16px;">
            <div class="helper-text">Session cookies remain on the target domain. Only the target profile is saved locally.</div>
            <div class="inline-group">
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
