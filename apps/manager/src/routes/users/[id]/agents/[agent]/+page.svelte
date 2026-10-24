<svelte:options runes={false} />
<script lang="ts">
  import AgentAccess from '#lib/components/agents/AgentAccess.svelte';
  import { createPageStore } from '#lib/stores/page.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  const page = createPageStore();
</script>

{#if $selectedTarget && $targetState.session.authenticated}
  {#key `${$selectedTarget.id}:${$selectedTarget.baseUrl}:${$page.params.id}:${$page.params.agent ?? ''}`}
    <AgentAccess target={$selectedTarget} userId={$page.params.id!} agentId={$page.params.agent!} />
  {/key}
{:else}
  <div class="notice-card">Connect this target to manage agents.</div>
{/if}
