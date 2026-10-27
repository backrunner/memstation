<script lang="ts">
  import { page } from '$app/state';
  import PageHeader from '#lib/components/layout/PageHeader.svelte';
  import MemoryEditor from '#lib/components/workspaces/MemoryEditor.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
</script>
<section class="page-stack">
  <PageHeader title="New key memory" />
  <a class="text-link" href={`${base}/keys`}>All workspace key memories</a>
  <WorkspaceReadStatus failure={data.creation.failure} />
  {#if data.workspaceResult.value && data.creation.value}
    {#key JSON.stringify([data.routeTarget.id, data.routeTarget.baseUrl, data.workspaceResult.value.id, 'new'])}
      <MemoryEditor scope={{ target: data.routeTarget, workspace: data.workspaceResult.value }} keyMemory  />
    {/key}
  {/if}
</section>
