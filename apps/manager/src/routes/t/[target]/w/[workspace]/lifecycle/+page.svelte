<script lang="ts">
 import MemoryOperations from '#lib/components/workspaces/MemoryOperations.svelte';
 import type {PageData} from './$types';
 let {data}:{data:PageData}=$props();
</script>
{#key `${data.routeTarget.id}:${data.workspaceResult.value?.id}:${data.partition}`}
 <MemoryOperations {data} />
{/key}
