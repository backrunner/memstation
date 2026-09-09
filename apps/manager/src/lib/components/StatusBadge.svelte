<script lang="ts">
  export let tone: 'brand' | 'success' | 'warning' | 'danger' | 'muted' = 'muted';
  export let label = '';
</script>

<span class={`status-badge ${tone}`}>
  {label}
</span>
