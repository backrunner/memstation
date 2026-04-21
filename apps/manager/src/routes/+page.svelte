<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';

  onMount(() => {
    void goto('/dashboard');
  });
</script>

<div class="hero-card">
  <span class="sidebar-label">Redirecting</span>
  <h1 class="page-title">Opening Workspace</h1>
  <p class="page-description">If the redirect does not happen automatically, open the dashboard from the sidebar.</p>
</div>
