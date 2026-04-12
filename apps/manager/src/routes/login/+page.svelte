<svelte:options runes={false} />

<script lang="ts">
  import { goto } from '$app/navigation';

  import { loginAdmin } from '$lib/api/client';
  import { selectedTarget, stageTargetAdminKey, targetState, updateSession } from '$lib/stores/targets';

  let token = '';
  let error = '';
  let submitting = false;

  async function submit(): Promise<void> {
    if (!$selectedTarget || !token.trim()) {
      error = 'Enter the administrator token for the selected MemStation target.';
      return;
    }

    submitting = true;
    error = '';
    try {
      const adminToken = token.trim();
      await loginAdmin($selectedTarget, adminToken);
      stageTargetAdminKey($selectedTarget.id, adminToken);
      updateSession({ authenticated: true, checking: false, checkedAt: new Date().toISOString() });
      token = '';
      await goto('/dashboard');
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Login failed';
    } finally {
      submitting = false;
    }
  }
</script>

<section class="page-stack">
  <div class="hero-card">
    <span class="sidebar-label">Admin Access</span>
    <h1 class="page-title">Sign in to the selected MemStation target</h1>
    <p class="page-description">
      The manager never stores the admin token in browser storage. The token stays in memory for the current tab and is also exchanged for the target's admin session cookie when supported.
    </p>
  </div>

  <div class="grid-2">
    <section class="card">
      <div class="page-header">
        <div>
          <h2 class="page-title" style="font-size: 1.35rem;">Login</h2>
          <p class="page-description">Target: {$selectedTarget?.label ?? 'None selected'} · {$selectedTarget?.baseUrl ?? 'N/A'}</p>
        </div>
      </div>

      <div class="toolbar">
        <label class="field">
          <span class="field-label">Admin token</span>
          <input class="input" bind:value={token} type="password" autocomplete="off" placeholder="MEMSTATION_ADMIN_TOKEN" />
        </label>
        {#if error}
          <div class="error-state">{error}</div>
        {/if}
        <button class="button" type="button" disabled={submitting || !$selectedTarget} on:click={submit}>
          {submitting ? 'Signing in...' : 'Start admin session'}
        </button>
      </div>
    </section>

    <section class="card">
      <div class="sidebar-label">Current session</div>
      <div class="toolbar" style="margin-top: 12px;">
        <div class="notice-card">
          {#if $targetState.session.authenticated}
            Active admin session detected for this target. You can return to the dashboard immediately.
          {:else if $targetState.session.checking}
            Checking the target session state.
          {:else}
            No admin session is currently active for this target.
          {/if}
        </div>
        <a class="button-secondary" href="/dashboard">Go to dashboard</a>
      </div>
    </section>
  </div>
</section>
