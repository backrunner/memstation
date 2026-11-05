<script lang="ts">
  import type { EntityRecallDiagnostics } from '#lib/types.ts';
  let { diagnostics }: { diagnostics?: EntityRecallDiagnostics } = $props();
  let message = $derived(diagnostics?.status === 'timed_out'
    ? 'Entity matching timed out. Results use the available keyword and vector channels.'
    : diagnostics?.status === 'unavailable'
      ? 'Entity matching is unavailable. Results use the available keyword and vector channels.'
      : diagnostics?.status === 'budget_exceeded'
        ? 'Entity matching was skipped because the question exceeds its input limit.' : '');
</script>

{#if message}<p class="helper-text" role="status">{message}</p>{/if}
