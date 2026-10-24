<svelte:options runes={false} />
<script lang="ts">
  import AgentDirectory from '#lib/components/agents/AgentDirectory.svelte';
  import { createPageStore } from '#lib/stores/page.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  const page = createPageStore();
</script>

{#if $selectedTarget && $targetState.session.authenticated}
  {#key `${$selectedTarget.id}:${$selectedTarget.baseUrl}:${$page.params.id}:${$page.params.agent ?? ''}`}
    <AgentDirectory target={$selectedTarget} userId={$page.params.id!}  />
  {/key}
{:else}
  <div class="notice-card">Connect this target to manage agents.</div>
{/if}
