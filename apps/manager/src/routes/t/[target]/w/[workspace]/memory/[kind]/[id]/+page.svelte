<script lang="ts">
  import MetadataRow from '#lib/components/MetadataRow.svelte';
  import { legacyReviewURL } from '#lib/scope/navigation.ts';
  import { memoryRouteKindFromDocumentType } from '#lib/memory/routes.ts';
  import { page } from '$app/state';
  import MemoryEditor from '#lib/components/workspaces/MemoryEditor.svelte';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import CollapsibleSection from '#lib/components/CollapsibleSection.svelte';
  import JsonView from '#lib/components/JsonView.svelte';
  import WorkspaceReadStatus from '#lib/components/workspaces/WorkspaceReadStatus.svelte';
  import { workspaceBaseURL, workspaceConversationURL, workspaceMemoryURL } from '#lib/scope/routes.ts';
  import type { PageData } from './$types';
  let { data }: { data: PageData } = $props();
  let base = $derived(workspaceBaseURL(data.routeTarget.id, page.params.workspace!));
  let generation = $derived(data.record.value?.generation_id || data.generation);
</script>

<section class="page-stack">
  <PageHeader title="Memory document" subtitle={page.params.id} />
  <a class="text-link" href={`${base}/memory${data.generation ? `?generation_id=${encodeURIComponent(data.generation)}` : ''}`}>All workspace memory documents</a>
  <WorkspaceReadStatus failure={data.record.failure} />
  {#if data.record.value}
    {@const item = data.record.value}
    <MetadataRow items={[item.type, item.updated_at ? new Date(item.updated_at).toLocaleString() : 'No update time']} />
    {#if item.lineage?.current_head_id && item.lineage.current_head_id !== item.id}
      <p class="notice-card">Historical fact. <a href={workspaceMemoryURL(base, 'fact', item.lineage.current_head_id, generation)}>Open current fact</a></p>
    {/if}
    {#if item.type !== 'segment' && item.updated_at && item.generation_id && data.workspaceResult.value && !(item.lineage?.current_head_id && item.lineage.current_head_id !== item.id) && item.metadata?.is_superseded !== true && !item.metadata?.superseded_by_id}
      {#key JSON.stringify([data.routeTarget.id, data.routeTarget.baseUrl, data.workspaceResult.value.id, item.type, item.id, item.generation_id])}
        <MemoryEditor scope={{ target: data.routeTarget, workspace: data.workspaceResult.value }} record={item} />
      {/key}
    {:else}<div class="memory-content">{item.content}</div>{/if}
    {#if item.conversation_id}<a class="text-link" href={workspaceConversationURL(base, item.conversation_id, generation)}>Open source conversation</a>{/if}
    {#if item.related?.length}
      <section class="page-stack"><h2 class="section-title">Related memories</h2>
        {#each item.related as related}
          {#if related.document_id && related.document_type}
            <a class="text-link" href={workspaceMemoryURL(base, related.document_type, related.document_id, generation)}><MetadataRow items={[related.relation_type || 'Related', related.document_type, related.document_id]} /></a>
          {:else}<span class="subtle-text"><MetadataRow items={[related.relation_type || 'Related', related.node_id || 'Unresolved node']} /></span>{/if}
        {/each}
      </section>
    {/if}
    {#if item.evidences?.length}
      <section class="page-stack"><h2 class="section-title">Evidence</h2>
        {#each item.evidences as evidence}
          <div class="evidence-row"><p class="subtle-text">{evidence.evidence_kind === 'context_reference' ? 'Context reference (not supporting evidence)' : evidence.evidence_kind}</p><div class="memory-content">{evidence.excerpt_text || 'No excerpt retained.'}</div></div>
        {/each}
      </section>
    {/if}
    {#if memoryRouteKindFromDocumentType(item.type)}<a class="text-link" href={legacyReviewURL(data.routeTarget.id, `/memory/${memoryRouteKindFromDocumentType(item.type)}/${encodeURIComponent(item.id)}`, item.user_id, generation)}>Review history and metadata</a>{/if}
    <CollapsibleSection title="Record details"><JsonView value={{ node_id: item.node_id, generation_id: item.generation_id, importance: item.importance, decay_factor: item.decay_factor, lineage: item.lineage, metadata: item.metadata, evidences: item.evidences }} /></CollapsibleSection>
  {/if}
</section>

<style>.memory-content { white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.7; } .evidence-row { padding: 12px 0; border-bottom: 1px solid var(--border-subtle); }</style>
