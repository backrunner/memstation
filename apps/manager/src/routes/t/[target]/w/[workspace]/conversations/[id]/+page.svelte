<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import { legacyReviewURL } from '#lib/scope/navigation.ts';
  import { page } from '$app/state';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import ChatMessageCard from '#lib/components/ChatMessageCard.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import JsonView from '#lib/components/JsonView.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL, workspaceMemoryURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
</script>

<section class="page-stack">
  <PageHeader title="Conversation" subtitle={page.params.id} />
  <a class="text-link" href={`${base}/conversations`}>All workspace conversations</a>
  <WorkspaceReadStatus failure={data.record.failure} />
  {#if data.record.value}
    {@const item = data.record.value}
    <MetadataRow items={[`${item.messages?.length ?? 0} messages`, `${item.usage.total_tokens} tokens`, item.finalize_status || 'unknown']} />
    {#if data.generation}<p class="helper-text">Memory generation: {data.generation}</p>{/if}
    <section class="page-stack" aria-label="Conversation messages">
      {#each item.messages ?? [] as message, index}<ChatMessageCard message={{ ...message, id: `${item.conversation_id}:${index}` }} />{/each}
      {#if !item.messages?.length}<p class="notice-card">No retained messages in this conversation.</p>{/if}
    </section>
    <section class="page-stack" aria-label="Conversation memory">
      <h2 class="section-title">Linked memory documents</h2>
      <WorkspaceReadStatus failure={data.memories.failure} />
      {#if data.memories.value}
        {#each data.memories.value.documents ?? [] as memory (`${memory.type}:${memory.id}`)}
          <a class="memory-row" href={workspaceMemoryURL(base, memory.type, memory.id, memory.generation_id || data.memories.value.generation_id)}><MetadataRow items={[memory.type, memory.id]} /><span>{memory.content}</span></a>
        {/each}
        {#if !data.memories.value.documents?.length}<p class="helper-text">No memory documents linked in this generation.</p>{/if}
      {/if}
    </section>
    <a class="text-link" href={legacyReviewURL(data.routeTarget.id, `/conversations/${encodeURIComponent(item.conversation_id)}`, item.user_id, data.generation)}>Processing and rebuild controls</a>
    <CollapsibleSection title="Processing and source details">
      <MetadataRow items={[`Summary: ${item.summary_status || 'unknown'}`, `Analysis: ${item.analysis_status || 'unknown'}`]} />
      <JsonView value={{ metadata: item.metadata, segments: item.segments, parts: item.parts, artifacts: item.artifacts }} />
    </CollapsibleSection>
  {/if}
</section>

<style>.memory-row { display: grid; gap: 8px; padding: 16px 0; border-bottom: 1px solid var(--border-subtle); white-space: pre-wrap; overflow-wrap: anywhere; }</style>
