<script lang="ts">
  import { invalidate } from '$app/navigation';
  import Button from '#lib/components/ui/Button.svelte';
  let { failure }: { failure: string } = $props();
</script>

{#if failure}
  <div class="error-state" role="alert">{failure} <Button class="btn-secondary" onclick={() => Promise.all([invalidate('workspace:detail'), invalidate('workspace:read')])}>Retry</Button></div>
{/if}
