<script lang="ts">
  import { invalidate } from '$app/navigation';
  import Button from '#lib/components/Button.svelte';
  import WorkspaceSettings from '#lib/components/workspaces/WorkspaceSettings.svelte';
  import { workspaceDirectoryURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
</script>

{#if data.workspaceResult.value}
  {#key JSON.stringify([data.routeTarget.id, data.routeTarget.baseUrl, data.workspaceResult.value.id])}
    <WorkspaceSettings scope={{ target: data.routeTarget, workspace: data.workspaceResult.value }} />
  {/key}
{:else}
  <section class="page-stack">
    <div class="error-state" role="alert">{data.workspaceResult.failure} <Button class="btn-secondary" onclick={() => invalidate('workspace:detail')}>Retry</Button></div>
    <a class="text-link" href={workspaceDirectoryURL(data.routeTarget.id)}>All workspaces</a>
  </section>
{/if}
