<svelte:options runes={false} />

<script lang="ts">
  import { browser } from '$app/environment';
  import { env } from '$env/dynamic/public';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { Network } from 'lucide-svelte';

  import { fetchHealth, loginAdmin } from '$lib/api/client';
  import TextInputField from '$lib/components/TextInputField.svelte';
  import { rememberTargetCredential } from '$lib/targets/credentials';
  import { parseTargetLocator } from '$lib/targets/uri';
  import type { ParsedTargetLocator } from '$lib/targets/uri';
  import { markTargetSessionConnected, updateSession, upsertTarget } from '$lib/stores/targets';

  let connectionInput = defaultConnectionInput();
  let adminToken = '';
  let label = '';
  let error = '';
  let submitting = false;
  let hydrated = false;

  type LocatorPreview = Partial<ParsedTargetLocator> & { error?: string };

  $: locatorPreview = inspectLocator(connectionInput);
  $: resolvedTarget = locatorPreview.target;
  $: resolvedAdminKey = locatorPreview.adminKey ?? adminToken.trim();

  onMount(() => {
    hydrated = true;
  });

  function defaultConnectionInput(): string {
    const configuredTarget = env.PUBLIC_MEMSTATION_DEFAULT_TARGET?.trim();
    if (configuredTarget) {
      try {
        return parseTargetLocator(configuredTarget).target.baseUrl ?? configuredTarget;
      } catch {
        return configuredTarget;
      }
    }
    if (browser && window.location.hostname) {
      return `http://${window.location.hostname}:8080`;
    }
    return 'http://127.0.0.1:8080';
  }

  function inspectLocator(input: string): LocatorPreview {
    if (!input.trim()) {
      return {};
    }
    try {
      return parseTargetLocator(input);
    } catch (cause) {
      return {
        error: cause instanceof Error ? cause.message : 'Invalid connection URI'
      };
    }
  }

  async function submit(): Promise<void> {
    if (locatorPreview.error) {
      error = locatorPreview.error;
      return;
    }
    if (!resolvedTarget?.baseUrl) {
      error = 'Enter a MemStation endpoint.';
      return;
    }
    if (!resolvedAdminKey) {
      error = 'Admin token is required. A target cannot be connected without it.';
      return;
    }

    submitting = true;
    error = '';
    try {
      const targetToVerify = { baseUrl: resolvedTarget.baseUrl };
      const connection = await loginAdmin(targetToVerify, resolvedAdminKey);
      await fetchHealth(targetToVerify, '/health/ready');
      const savedTarget = upsertTarget({
        ...resolvedTarget,
        label: label.trim() || resolvedTarget.label
      });
      rememberTargetCredential(savedTarget.id, resolvedAdminKey);
      markTargetSessionConnected(savedTarget.id, connection.checkedAt);
      updateSession({ authenticated: true, checking: false, checkedAt: connection.checkedAt });
      await goto('/dashboard');
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Connection failed';
    } finally {
      submitting = false;
    }
  }
</script>

<section class="auth-shell connect-shell">
  <div class="auth-card connect-card">
    <div class="auth-brand">
      <span class="auth-brand-mark" aria-hidden="true"><Network size={18} strokeWidth={1.9} /></span>
      <span class="auth-brand-name">MemStation</span>
    </div>

    <h1 class="auth-title">Connect target</h1>

    <form class="auth-actions" on:submit|preventDefault={submit}>
      <TextInputField
        label="Connection URI or base URL"
        bind:value={connectionInput}
        placeholder="memstation://127.0.0.1:8080?label=Debug&key=..."
        inputClass="input-mono"
        error={locatorPreview.error}
      />

      <TextInputField label="Label" bind:value={label} placeholder={resolvedTarget?.label ?? 'MemStation Debug'} />

      {#if !locatorPreview.adminKey}
        <TextInputField
          label="Admin token"
          bind:value={adminToken}
          type="password"
          autocomplete="off"
          placeholder="MEMSTATION_ADMIN_TOKEN"
          inputClass="input-mono"
        />
      {/if}

      {#if error}
        <div class="error-state">{error}</div>
      {/if}

      <button class="btn-primary" type="submit" disabled={!hydrated || submitting}>
        {submitting ? 'Connecting…' : 'Connect target'}
      </button>
    </form>
  </div>
</section>
