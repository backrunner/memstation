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
    if (busy) return;
    dispatch('confirm');
  }
</script>

<AlertDialog.Root {open} onOpenChange={(nextOpen) => { if (!nextOpen) cancel(); }}>
  <AlertDialog.Portal>
    <AlertDialog.Overlay class="dialog-overlay" />
    <AlertDialog.Content class="dialog-card confirm-action-dialog" escapeKeydownBehavior={busy ? 'ignore' : 'close'} aria-busy={busy}>
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
    display: grid;
    gap: 20px;
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
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--text-muted);
    font-family: var(--font-sans);
    font-weight: 400;
  }

  .confirm-action-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
</style>
