<script lang="ts">
  import PageHeader from '#lib/components/PageHeader.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
</script>
<section class="page-stack"><PageHeader title="Open workspace" /><WorkspaceReadStatus failure={data.failure ?? ''} /></section>
