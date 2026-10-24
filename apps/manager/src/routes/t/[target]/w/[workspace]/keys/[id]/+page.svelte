<script lang="ts">
  import { legacyReviewURL } from '#lib/scope/navigation.ts';
  import { page } from '$app/state';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import MemoryEditor from '#lib/components/workspaces/MemoryEditor.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
</script>
<section class="page-stack">
  <PageHeader title={data.record.value?.key ?? 'Key memory'} />
  <a class="text-link" href={`${base}/keys`}>All workspace key memories</a>
  <WorkspaceReadStatus failure={data.record.failure} />
  {#if data.workspaceResult.value && data.record.value}
    {#key JSON.stringify([data.routeTarget.id, data.routeTarget.baseUrl, data.workspaceResult.value.id, data.record.value.id])}
      <MemoryEditor scope={{ target: data.routeTarget, workspace: data.workspaceResult.value }} keyMemory record={data.record.value} />
    {/key}
    <a class="text-link" href={legacyReviewURL(data.routeTarget.id, `/memory/keys/${encodeURIComponent(data.record.value.id)}`, data.workspaceResult.value.user_id)}>Review history and metadata</a>
  {/if}
</section>
