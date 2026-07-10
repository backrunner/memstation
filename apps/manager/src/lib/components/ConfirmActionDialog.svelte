<svelte:options runes={false} />

<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { AlertDialog } from 'bits-ui';

  export let open = false;
  export let title = 'Confirm action';
  export let description = '';
  export let subject = '';
  export let confirmLabel = 'Confirm';
  export let busy = false;
  export let tone: 'danger' | 'primary' = 'danger';

  const dispatch = createEventDispatcher<{ confirm: void; cancel: void }>();

  function cancel(): void {
    if (busy) return;
    open = false;
    dispatch('cancel');
  }

  function confirm(): void {
    dispatch('confirm');
  }
</script>

<AlertDialog.Root bind:open>
  <AlertDialog.Portal>
    <AlertDialog.Overlay class="dialog-overlay" />
    <AlertDialog.Content class="dialog-card confirm-action-dialog">
      <div class="confirm-action-copy">
        <AlertDialog.Title class="section-title" level={3}>{title}</AlertDialog.Title>
        <AlertDialog.Description class="panel-meta">{description}</AlertDialog.Description>
      </div>

      {#if subject}
        <div class="confirm-action-subject mono">{subject}</div>
      {/if}

      <div class="confirm-action-audit">This action is recorded in Audit.</div>

      <div class="confirm-action-actions">
        <button class="btn-secondary" type="button" disabled={busy} on:click={cancel}>Cancel</button>
        <button
          class={tone === 'danger' ? 'btn-danger' : 'btn-primary'}
          type="button"
          disabled={busy}
          on:click={confirm}
        >{busy ? 'Working…' : confirmLabel}</button>
      </div>
    </AlertDialog.Content>
  </AlertDialog.Portal>
</AlertDialog.Root>

<style>
  :global(.confirm-action-dialog) {
    width: min(440px, calc(100vw - 32px));
  }

  .confirm-action-copy {
    display: grid;
    gap: 6px;
  }

  .confirm-action-subject,
  .confirm-action-audit {
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
    color: var(--text-secondary);
    font-size: var(--font-size-sm);
    overflow-wrap: anywhere;
  }

  .confirm-action-audit {
    border-color: var(--warning-soft);
    background: var(--warning-soft);
    color: var(--warning);
    font-family: var(--font-sans);
    font-weight: 600;
  }

  .confirm-action-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
</style>
