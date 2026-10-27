<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import BrandMark from '#lib/components/BrandMark.svelte';

  import { fetchHealth, loginAdmin } from '#lib/api/client.ts';
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import { parseTargetLocator } from '#lib/targets/uri.ts';
  import type { ParsedTargetLocator } from '#lib/targets/uri.ts';
  import { availableTargets, markTargetSessionConnected, updateSession, upsertTarget } from '#lib/stores/targets.ts';

  let connectionInput = '';
  let initialTargetSet = false;
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

  $: if (hydrated && !initialTargetSet && $availableTargets.length) {
    connectionInput = $availableTargets[0].baseUrl;
    initialTargetSet = true;
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
      adminToken = '';
      connectionInput = resolvedTarget.baseUrl;
      markTargetSessionConnected(savedTarget.id, connection.checkedAt);
      updateSession({ authenticated: true, checking: false, checkedAt: connection.checkedAt });
      await goto('/dashboard');
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Connection failed';
    } finally {
      adminToken = '';
      if (resolvedTarget?.baseUrl) connectionInput = resolvedTarget.baseUrl;
      submitting = false;
    }
  }
</script>

<section class="auth-shell connect-shell">
  <div class="auth-card connect-card">
    <div class="auth-brand">
      <BrandMark size={36} />
      <span class="auth-brand-name">MemStation</span>
    </div>

    <h1 class="auth-title">Connect target</h1>

    {#if $availableTargets.length === 0}
      <div class="error-state">No services are enabled in this Manager. Configure a service before connecting.</div>
    {/if}
    <form class="auth-actions" on:submit|preventDefault={submit}>
      {#if $availableTargets.length > 1}
        <SelectField label="Available service" value={$availableTargets.find((item) => item.baseUrl === resolvedTarget?.baseUrl)?.id ?? ''}
          items={$availableTargets.map((item) => ({ value: item.id, label: item.label }))}
          onValueChange={(id) => { const target = $availableTargets.find((item) => item.id === id); if (target) { connectionInput = target.baseUrl; label = target.label; adminToken = ''; } }} />
      {/if}
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

      <Button class="btn-primary" type="submit" disabled={!hydrated || submitting || !$availableTargets.length}>
        {submitting ? 'Connecting…' : 'Connect target'}
      </Button>
    </form>
  </div>
</section>
