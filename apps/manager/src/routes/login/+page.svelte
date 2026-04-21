<svelte:options runes={false} />

<script lang="ts">
  import { goto } from '$app/navigation';

  import { loginAdmin } from '$lib/api/client';
  import TextInputField from '$lib/components/TextInputField.svelte';
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
    <h1 class="page-title">Sign In</h1>
  </div>

  <section class="card login-card">
    <div class="page-header">
      <div>
        <h2 class="section-title">{$selectedTarget?.label ?? 'None Selected'}</h2>
        <div class="helper-text" style="margin-top: 4px;">{$selectedTarget?.baseUrl ?? 'Select a target first.'}</div>
      </div>
      {#if $targetState.session.authenticated}
        <a class="button-secondary" href="/dashboard">Open dashboard</a>
      {/if}
    </div>

    <div class="toolbar">
      <TextInputField
        label="Admin Token"
        bind:value={token}
        type="password"
        autocomplete="off"
        placeholder="MEMSTATION_ADMIN_TOKEN"
      />
      <div class="helper-text">Token stays in memory for the current tab only.</div>
      {#if error}
        <div class="error-state">{error}</div>
      {/if}
      {#if $targetState.session.authenticated}
        <div class="notice-card">An admin session is already active for this target.</div>
      {:else if $targetState.session.checking}
        <div class="notice-card">Checking current session…</div>
      {/if}
      <div class="inline-group" style="justify-content: flex-end;">
        <button class="button" type="button" disabled={submitting || !$selectedTarget} on:click={submit}>
          {submitting ? 'Signing in...' : 'Start admin session'}
        </button>
      </div>
    </div>
  </section>
</section>

<style>
  .login-card {
    max-width: 720px;
  }
</style>
