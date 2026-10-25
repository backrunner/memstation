<script lang="ts">
  import { page } from '$app/state';
  import { invalidate } from '$app/navigation';
  import AgentDirectory from '#lib/components/agents/AgentDirectory.svelte';
  import AgentAccess from '#lib/components/agents/AgentAccess.svelte';
  import Button from '#lib/components/Button.svelte';
  import { workspaceDirectoryURL, workspaceURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let workspace = $derived(data.workspaceResult.value);
  let base = $derived(workspaceURL(data.routeTarget.id, page.params.workspace!).replace(/settings$/, 'access'));
</script>

{#if workspace}
  {#key JSON.stringify([data.routeTarget.id, data.routeTarget.baseUrl, workspace.id, workspace.user_id, page.params.agent])}
    {#if page.params.agent}
      <AgentAccess target={data.routeTarget} userId={workspace.user_id} workspaceId={workspace.id} agentId={page.params.agent} directoryPath={base} />
    {:else}
      <AgentDirectory target={data.routeTarget} userId={workspace.user_id} workspaceName={workspace.name} workspaceId={workspace.id} allowedCapabilities={workspace.capabilities} basePath={base} />
    {/if}
  {/key}
{:else}
  <section class="page-stack">
    <div class="error-state" role="alert">{data.workspaceResult.failure} <Button class="btn-secondary" onclick={() => invalidate('workspace:detail')}>Retry</Button></div>
    <a class="text-link" href={workspaceDirectoryURL(data.routeTarget.id)}>All workspaces</a>
  </section>
{/if}
