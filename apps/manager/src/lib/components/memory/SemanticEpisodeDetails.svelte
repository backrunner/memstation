<script lang="ts">
  let { metadata }: { metadata?: Record<string, unknown> | null } = $props();
  let participants = $derived(Array.isArray(metadata?.participants) ? metadata.participants.filter((value): value is string => typeof value === 'string') : []);
  let state = $derived(typeof metadata?.status === 'string' ? metadata.status : 'unspecified');
  let sessionDate = $derived(typeof metadata?.source_session_time === 'string' ? metadata.source_session_time : '');
  let anchor = $derived(metadata?.time_anchor && typeof metadata.time_anchor === 'object' && 'text' in metadata.time_anchor && typeof metadata.time_anchor.text === 'string' ? metadata.time_anchor.text : 'No explicit time anchor');
  let sourceTimes = $derived(metadata?.source_times && typeof metadata.source_times === 'object' ? [...new Set(Object.values(metadata.source_times).filter((value): value is string => typeof value === 'string'))] : []);
</script>

<section aria-label="Semantic episode context" class="episode-details">
  <h2 class="section-title">Episode context</h2>
  <dl>
    <div><dt>Event state</dt><dd>{state.replaceAll('_', ' ')}</dd></div>
    <div><dt>Participants</dt><dd>{participants.length ? participants.join(', ') : 'Not identified in the source'}</dd></div>
    <div><dt>Source time wording</dt><dd>{anchor}</dd></div>
    {#if sessionDate}<div><dt>Source session date</dt><dd>{sessionDate}</dd></div>{/if}
    {#if sourceTimes.length}<div><dt>Statement time</dt><dd>{#each sourceTimes as value}<time datetime={value}>{value}</time>{/each}</dd></div>{/if}
  </dl>
  <p class="helper-text">This narrative is extracted from cited sources. Relative time wording and statement timestamps are retained separately.</p>
</section>

<style>
  .episode-details { display: grid; gap: 12px; }
  dl { margin: 0; }
  dl > div { display: grid; grid-template-columns: minmax(120px, 180px) minmax(0, 1fr); gap: 12px; padding: 10px 0; border-bottom: 1px solid var(--border-subtle); }
  dt { color: var(--text-muted); }
  dd { margin: 0; overflow-wrap: anywhere; white-space: pre-wrap; }
  time { display: block; }
  @media (max-width: 480px) { dl > div { grid-template-columns: 1fr; gap: 4px; } }
</style>
