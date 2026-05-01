<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';

  onMount(() => {
    void goto('/dashboard');
  });
</script>

<section class="page-stack">
  <div class="notice-card">Redirecting to dashboard…</div>
</section>
