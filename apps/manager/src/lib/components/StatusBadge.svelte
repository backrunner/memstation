<script lang="ts">
  export let tone: 'brand' | 'success' | 'warning' | 'danger' | 'muted' = 'muted';
  export let label = '';
  export let withDot = false;
</script>

<span class={`status-badge ${tone}`}>
  {#if withDot}<span class="status-badge-dot" aria-hidden="true"></span>{/if}
  {label}
</span>
