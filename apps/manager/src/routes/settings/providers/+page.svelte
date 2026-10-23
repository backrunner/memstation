<svelte:options runes={false} />

<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import { onMount } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { PencilLine, Plus, TriangleAlert } from '@lucide/svelte';

  import { fetchConfig, fetchProviderDiagnostics, saveConfig } from '#lib/api/client.ts';
  import PageHeader from '#lib/components/PageHeader.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import StatusBadge from '#lib/components/StatusBadge.svelte';
  import TextInputField from '#lib/components/TextInputField.svelte';
  import { isHTTPURL } from '#lib/forms/validation.ts';
  import {
    buildSessionDraftKey,
    clearSessionDraft,
    draftsEqual,
    readSessionDraft,
    useSessionDraftPersistence,
    useSessionDraftUnloadWarning,
    writeSessionDraft
  } from '#lib/stores/session-drafts.ts';
  import { selectedTarget, targetState } from '#lib/stores/targets.ts';
  import type { AdminConfigPayload, ProviderDiagnostic, ProviderSetPayload, ProviderSummary, SystemRuntimeConfig } from '#lib/types.ts';

  type ProviderScope = 'gateway' | 'internal' | 'embedding' | 'rerank';

  type ProviderForm = {
    scope: ProviderScope;
    provider: string;
    baseUrl: string;
    apiKey: string;
    hasAPIKey: boolean;
    source: string;
    updatedAt?: string;
  };

  type VisibleProviderMap = Record<ProviderScope, string[]>;
  type ProviderSettingsDraft = {
    forms: ProviderForm[];
    visibleProviders: VisibleProviderMap;
    gatewayDefaultProvider: string;
    internalDefaultProvider: string;
    providerDialogOpen: boolean;
    editingFormKey: string;
    draftBaseUrl: string;
    draftAPIKey: string;
    addProviderDialogOpen: boolean;
    addProviderScope: ProviderScope | null;
    addProviderValue: string;
  };

  const scopeOrder: ProviderScope[] = ['gateway', 'internal', 'embedding', 'rerank'];
  const providerSettingsDraftVersion = 1;

  let loading = true;
  let saving = false;
  let error = '';
  let success = '';
  let lastLoadKey = '';
  let visibleProvidersKey = '';
  let config: AdminConfigPayload | null = null;
  let forms: ProviderForm[] = [];
  let diagnostics: ProviderDiagnostic[] = [];
  let visibleProviders = emptyVisibleProviders();
  let gatewayDefaultProvider = '';
  let internalDefaultProvider = '';
  let fieldErrors: Record<string, string> = {};
  let providerDialogOpen = false;
  let editingForm: ProviderForm | null = null;
  let draftBaseUrl = '';
  let draftAPIKey = '';
  let dialogError = '';
  let addProviderDialogOpen = false;
  let addProviderScope: ProviderScope | null = null;
  let addProviderValue = '';
  let activeDraftKey = '';
  let hydratingDraft = false;
  let draftReady = false;
  let savedDraft = emptyDraft();
  let currentDraft = emptyDraft();

  onMount(async () => {
    await loadConfig();
  });

  $: loadKey = `${$selectedTarget?.id ?? 'none'}:${$targetState.session.authenticated ? 'auth' : 'anon'}`;
  $: providersDraftKey = buildSessionDraftKey('settings.providers', $selectedTarget?.id);
  $: if (loadKey !== lastLoadKey && !$targetState.session.checking) {
    lastLoadKey = loadKey;
    void loadConfig();
  }
  $: currentDraft = captureDraft();
  $: if (draftReady && activeDraftKey && !hydratingDraft) {
    if (draftsEqual(currentDraft, savedDraft)) {
      clearSessionDraft(activeDraftKey);
    } else {
      writeSessionDraft(activeDraftKey, providerSettingsDraftVersion, currentDraft);
    }
  }

  $: if (!providerDialogOpen && editingForm) {
    resetProviderDialogState();
  }

  $: if (!addProviderDialogOpen && addProviderScope) {
    resetAddProviderDialog();
  }

  useSessionDraftPersistence(() => {
    persistDraftSnapshot();
  });

  useSessionDraftUnloadWarning(
    () => draftReady && !draftsEqual(currentDraft, savedDraft),
    'These provider changes have not been saved to the selected target. Closing or reloading the page may discard the draft kept in this tab.'
  );

  function emptyVisibleProviders(): VisibleProviderMap {
    return {
      gateway: [],
      internal: [],
      embedding: [],
      rerank: []
    };
  }

  function emptyDraft(): ProviderSettingsDraft {
    return {
      forms: [],
      visibleProviders: emptyVisibleProviders(),
      gatewayDefaultProvider: '',
      internalDefaultProvider: '',
      providerDialogOpen: false,
      editingFormKey: '',
      draftBaseUrl: '',
      draftAPIKey: '',
      addProviderDialogOpen: false,
      addProviderScope: null,
      addProviderValue: ''
    };
  }

  function cloneVisibleProviders(value: VisibleProviderMap = emptyVisibleProviders()): VisibleProviderMap {
    return {
      gateway: [...(value.gateway ?? [])],
      internal: [...(value.internal ?? [])],
      embedding: [...(value.embedding ?? [])],
      rerank: [...(value.rerank ?? [])]
    };
  }

  function captureDraft(): ProviderSettingsDraft {
    return {
      forms: forms.map((form) => ({ ...form })),
      visibleProviders: cloneVisibleProviders(visibleProviders),
      gatewayDefaultProvider,
      internalDefaultProvider,
      providerDialogOpen,
      editingFormKey: editingForm ? formKey(editingForm.scope, editingForm.provider) : '',
      draftBaseUrl,
      draftAPIKey,
      addProviderDialogOpen,
      addProviderScope,
      addProviderValue
    };
  }

  function baselineDraft(): ProviderSettingsDraft {
    return {
      forms: forms.map((form) => ({ ...form })),
      visibleProviders: cloneVisibleProviders(visibleProviders),
      gatewayDefaultProvider,
      internalDefaultProvider,
      providerDialogOpen: false,
      editingFormKey: '',
      draftBaseUrl: '',
      draftAPIKey: '',
      addProviderDialogOpen: false,
      addProviderScope: null,
      addProviderValue: ''
    };
  }

  function persistDraftSnapshot(): void {
    if (!activeDraftKey) {
      return;
    }
    const snapshot = captureDraft();
    if (draftsEqual(snapshot, savedDraft)) {
      clearSessionDraft(activeDraftKey);
      return;
    }
    writeSessionDraft(activeDraftKey, providerSettingsDraftVersion, snapshot);
  }

  function sanitizeVisibleProviders(value: VisibleProviderMap | undefined, nextForms: ProviderForm[] = forms): VisibleProviderMap {
    const next = emptyVisibleProviders();
    for (const scope of scopeOrder) {
      const scopeForms = nextForms.filter((form) => form.scope === scope);
      const available = new Set(scopeForms.map((form) => form.provider));
      const requested = (value?.[scope] ?? []).filter((provider) => available.has(provider));
      const activeProvider = activeProviderForScope(scope);
      next[scope] = requested.length > 0 ? requested : defaultVisibleProvidersForScope(scopeForms, activeProvider);
    }
    return next;
  }

  function applyDraft(draft: ProviderSettingsDraft | null): void {
    if (!draft) {
      return;
    }

    const nextForms = forms.map((form) => {
      const match = draft.forms.find((candidate) => candidate.scope === form.scope && candidate.provider === form.provider);
      return match ? { ...form, ...match } : form;
    });
    forms = nextForms;
    visibleProviders = sanitizeVisibleProviders(draft.visibleProviders, nextForms);

    const gatewayProviders = new Set(nextForms.filter((form) => form.scope === 'gateway').map((form) => form.provider));
    const internalProviders = new Set(nextForms.filter((form) => form.scope === 'internal').map((form) => form.provider));
    gatewayDefaultProvider = gatewayProviders.has(draft.gatewayDefaultProvider) ? draft.gatewayDefaultProvider : gatewayDefaultProvider;
    internalDefaultProvider = internalProviders.has(draft.internalDefaultProvider) ? draft.internalDefaultProvider : internalDefaultProvider;

    const nextEditingForm =
      nextForms.find((form) => formKey(form.scope, form.provider) === draft.editingFormKey) ?? null;
    editingForm = nextEditingForm ? { ...nextEditingForm } : null;
    providerDialogOpen = Boolean(draft.providerDialogOpen && nextEditingForm);
    draftBaseUrl = draft.draftBaseUrl ?? nextEditingForm?.baseUrl ?? '';
    draftAPIKey = draft.draftAPIKey ?? '';

    const nextAddScope = draft.addProviderScope && scopeOrder.includes(draft.addProviderScope) ? draft.addProviderScope : null;
    addProviderScope = nextAddScope;
    const addItems = nextAddScope ? availableProviderItems(nextAddScope) : [];
    addProviderValue = addItems.some((item) => item.value === draft.addProviderValue)
      ? draft.addProviderValue
      : addItems[0]?.value ?? '';
    addProviderDialogOpen = Boolean(draft.addProviderDialogOpen && addProviderScope && addProviderValue);
  }

  function normalizeProviderSets(configPayload: AdminConfigPayload | null): Record<ProviderScope, ProviderSummary[]> {
    const sets = (configPayload?.provider_sets ?? {}) as ProviderSetPayload;
    return {
      gateway: sets.gateway ?? ((configPayload?.providers as ProviderSummary[] | undefined) ?? []),
      internal: sets.internal ?? [],
      embedding: sets.embedding ?? [],
      rerank: sets.rerank ?? []
    };
  }

  function toForms(providerSets: Record<ProviderScope, ProviderSummary[]>): ProviderForm[] {
    const next: ProviderForm[] = [];
    for (const scope of scopeOrder) {
      for (const provider of providerSets[scope] ?? []) {
        next.push({
          scope,
          provider: provider.provider,
          baseUrl: provider.base_url,
          apiKey: '',
          hasAPIKey: provider.has_api_key,
          source: provider.source,
          updatedAt: provider.updated_at
        });
      }
    }
    return next;
  }

  function formsForScope(scope: ProviderScope): ProviderForm[] {
    return forms.filter((form) => form.scope === scope);
  }

  function defaultVisibleProvidersForScope(scopeForms: ProviderForm[], activeProvider = ''): string[] {
    const prioritized = scopeForms
      .filter((form) => form.provider === activeProvider || form.hasAPIKey || form.source === 'override')
      .map((form) => form.provider);

    if (activeProvider && scopeForms.some((form) => form.provider === activeProvider)) {
      prioritized.unshift(activeProvider);
    }

    const next = Array.from(new Set(prioritized));
    if (next.length > 0) {
      return next;
    }
    return scopeForms[0] ? [scopeForms[0].provider] : [];
  }

  function visibleFormsForScope(scope: ProviderScope): ProviderForm[] {
    const visible = new Set(visibleProviders[scope] ?? []);
    const activeProvider = activeProviderForScope(scope);
    if (activeProvider) {
      visible.add(activeProvider);
    }
    const next = formsForScope(scope).filter((form) => visible.has(form.provider));
    return next.length > 0 ? next : formsForScope(scope).slice(0, 1);
  }

  function hiddenFormsForScope(scope: ProviderScope): ProviderForm[] {
    const visible = new Set(visibleFormsForScope(scope).map((form) => form.provider));
    return formsForScope(scope).filter((form) => !visible.has(form.provider));
  }

  function providerItemsForScope(scope: ProviderScope): Array<{ value: string; label: string }> {
    return formsForScope(scope).map((form) => ({
      value: form.provider,
      label: form.provider
    }));
  }

  function availableProviderItems(scope: ProviderScope): Array<{ value: string; label: string }> {
    return hiddenFormsForScope(scope).map((form) => ({
      value: form.provider,
      label: form.provider
    }));
  }

  function inferredProviderFromModel(modelName: string, scope: ProviderScope, scopeForms: ProviderForm[] = formsForScope(scope)): string {
    const normalized = modelName.trim().toLowerCase();
    if (!normalized) {
      return '';
    }
    const supportedProviders = new Set(scopeForms.map((form) => form.provider));
    const directPrefix = normalized.split(':')[0] ?? '';
    if (supportedProviders.has(directPrefix)) {
      return directPrefix;
    }
    if (directPrefix === 'openai' && supportedProviders.has('openai_compatible')) {
      return 'openai_compatible';
    }
    if (scope === 'embedding' && normalized.includes('embedding') && supportedProviders.has('openai_compatible')) {
      return 'openai_compatible';
    }
    return '';
  }

  function activeProviderFromPayload(scope: ProviderScope, payload: AdminConfigPayload, nextForms: ProviderForm[]): string {
    const runtimeConfig = (payload.runtime ?? {}) as SystemRuntimeConfig;
    if (scope === 'gateway') {
      return String(runtimeConfig.default_provider ?? '');
    }
    if (scope === 'internal') {
      return String(runtimeConfig.internal_default_provider ?? '');
    }
    if (scope === 'embedding') {
      return inferredProviderFromModel(String(payload.embedding_model ?? ''), scope, nextForms);
    }
    return inferredProviderFromModel(String(runtimeConfig.rerank_model ?? ''), scope, nextForms);
  }

  function mergeVisibleProviders(base: VisibleProviderMap, nextForms: ProviderForm[], payload: AdminConfigPayload): VisibleProviderMap {
    const next = emptyVisibleProviders();
    for (const scope of scopeOrder) {
      const scopeForms = nextForms.filter((form) => form.scope === scope);
      const available = new Set(scopeForms.map((form) => form.provider));
      const retained = (base[scope] ?? []).filter((provider) => available.has(provider));
      const activeProvider = activeProviderFromPayload(scope, payload, scopeForms);
      const defaults = defaultVisibleProvidersForScope(scopeForms, activeProvider);
      next[scope] = Array.from(new Set([...retained, ...defaults]));
    }
    return next;
  }

  function applyConfig(payload: AdminConfigPayload, providerDiagnostics: ProviderDiagnostic[]): void {
    const runtimeConfig = (payload.runtime ?? {}) as SystemRuntimeConfig;
    const nextForms = toForms(normalizeProviderSets(payload));
    config = payload;
    diagnostics = providerDiagnostics;
    forms = nextForms;
    gatewayDefaultProvider = String(runtimeConfig.default_provider ?? nextForms.find((form) => form.scope === 'gateway')?.provider ?? '');
    internalDefaultProvider = String(runtimeConfig.internal_default_provider ?? nextForms.find((form) => form.scope === 'internal')?.provider ?? '');
    const nextVisibleKey = $selectedTarget?.id ?? '';
    const baseVisibleProviders = visibleProvidersKey === nextVisibleKey ? visibleProviders : emptyVisibleProviders();
    visibleProviders = mergeVisibleProviders(baseVisibleProviders, nextForms, payload);
    visibleProvidersKey = nextVisibleKey;
    fieldErrors = {};
    resetProviderDialogState();
    resetAddProviderDialog();
    savedDraft = baselineDraft();
    activeDraftKey = providersDraftKey;
    hydratingDraft = true;
    applyDraft(readSessionDraft<ProviderSettingsDraft>(providersDraftKey, providerSettingsDraftVersion));
    hydratingDraft = false;
    draftReady = true;
  }

  async function loadConfig(): Promise<void> {
    if (!$selectedTarget || !$targetState.session.authenticated) {
      loading = false;
      draftReady = false;
      return;
    }
    loading = true;
    error = '';
    success = '';
    try {
      const [configPayload, providerDiagnostics] = await Promise.all([
        fetchConfig($selectedTarget),
        fetchProviderDiagnostics($selectedTarget)
      ]);
      applyConfig(configPayload, providerDiagnostics);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to load provider settings';
    } finally {
      loading = false;
    }
  }

  function formKey(scope: ProviderScope, provider: string): string {
    return `${scope}:${provider}`;
  }

  function validate(): boolean {
    const nextErrors: Record<string, string> = {};
    for (const form of forms) {
      if (form.baseUrl.trim() && !isHTTPURL(form.baseUrl.trim())) {
        nextErrors[formKey(form.scope, form.provider)] = 'Base URL must use http:// or https://.';
      }
    }
    if (gatewayDefaultProvider && !formsForScope('gateway').some((form) => form.provider === gatewayDefaultProvider)) {
      nextErrors.gatewayDefault = 'Choose one gateway provider as the active route.';
    }
    if (internalDefaultProvider && !formsForScope('internal').some((form) => form.provider === internalDefaultProvider)) {
      nextErrors.internalDefault = 'Choose one internal provider as the active route.';
    }
    fieldErrors = nextErrors;
    return Object.keys(nextErrors).length === 0;
  }

  async function persist(): Promise<void> {
    if (!$selectedTarget || !config) {
      return;
    }
    if (!validate()) {
      error = 'Please fix the highlighted provider fields.';
      success = '';
      return;
    }
    saving = true;
    error = '';
    success = '';
    try {
      const currentSets = normalizeProviderSets(config);
      const nextSets: Record<string, Record<string, Record<string, string>>> = {};

      for (const scope of scopeOrder) {
        const scopePayload: Record<string, Record<string, string>> = {};
        const currentProviders = (currentSets[scope] ?? []).reduce<Record<string, ProviderSummary>>((acc, item) => {
          acc[item.provider] = item;
          return acc;
        }, {});
        for (const form of formsForScope(scope)) {
          const current = currentProviders[form.provider];
          const payload: Record<string, string> = {};
          if (form.baseUrl.trim() !== (current?.base_url ?? '')) {
            payload.base_url = form.baseUrl.trim();
          }
          if (form.apiKey.trim()) {
            payload.api_key = form.apiKey.trim();
          }
          if (Object.keys(payload).length > 0) {
            scopePayload[form.provider] = payload;
          }
        }
        if (Object.keys(scopePayload).length > 0) {
          nextSets[scope] = scopePayload;
        }
      }

      const payload = await saveConfig($selectedTarget, {
        provider_sets: nextSets,
        runtime: {
          default_provider: gatewayDefaultProvider,
          internal_default_provider: internalDefaultProvider
        }
      });
      const nextDiagnostics = await fetchProviderDiagnostics($selectedTarget);
      if (activeDraftKey) {
        clearSessionDraft(activeDraftKey);
      }
      applyConfig(payload, nextDiagnostics);
      success = 'Provider routes and credentials were applied to the selected target.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Failed to save provider settings';
    } finally {
      saving = false;
    }
  }

  function diagnosticFor(scope: ProviderScope, provider: string): ProviderDiagnostic | undefined {
    return diagnostics.find((item) => item.scope === scope && item.provider === provider);
  }

  function scopeTitle(scope: ProviderScope): string {
    switch (scope) {
      case 'gateway':
        return 'Gateway Providers';
      case 'internal':
        return 'Internal Providers';
      case 'embedding':
        return 'Embedding Providers';
      case 'rerank':
        return 'Rerank Providers';
    }
  }

  function scopeSupportsDefaultRoute(scope: ProviderScope): scope is 'gateway' | 'internal' {
    return scope === 'gateway' || scope === 'internal';
  }

  function activeProviderForScope(scope: ProviderScope): string {
    if (scope === 'gateway') {
      return gatewayDefaultProvider;
    }
    if (scope === 'internal') {
      return internalDefaultProvider;
    }
    if (scope === 'embedding') {
      return inferredProviderFromModel(String(config?.embedding_model ?? ''), scope);
    }
    return inferredProviderFromModel(String((config?.runtime as SystemRuntimeConfig | undefined)?.rerank_model ?? ''), scope);
  }

  function routeDisplayValue(scope: ProviderScope): string {
    const activeProvider = activeProviderForScope(scope);
    return activeProvider || 'Auto';
  }

  function providerEndpoint(form: ProviderForm, diagnostic?: ProviderDiagnostic): string {
    const value = form.baseUrl.trim() || diagnostic?.base_url || '';
    if (!value) {
      return 'No Endpoint';
    }
    try {
      const parsed = new URL(value);
      return `${parsed.host}${parsed.pathname === '/' ? '' : parsed.pathname}`;
    } catch {
      return value.replace(/^https?:\/\//, '');
    }
  }

  function primaryIssue(form: ProviderForm, diagnostic?: ProviderDiagnostic): string {
    const issue = diagnostic?.issues?.[0] ?? '';
    if (!issue) {
      return '';
    }
    const normalized = issue.toLowerCase();
    if (normalized.includes('api key missing') && !form.hasAPIKey) {
      return '';
    }
    return issue
      .replace('API key missing in fixture runtime', 'Runtime key missing')
      .replace('missing in fixture runtime', 'runtime missing');
  }

  function openProviderDialog(form: ProviderForm): void {
    editingForm = { ...form };
    draftBaseUrl = form.baseUrl;
    draftAPIKey = '';
    dialogError = '';
    providerDialogOpen = true;
  }

  function resetProviderDialogState(): void {
    editingForm = null;
    draftBaseUrl = '';
    draftAPIKey = '';
    dialogError = '';
  }

  function closeProviderDialog(): void {
    providerDialogOpen = false;
  }

  function applyDialogChanges(): void {
    if (!editingForm) {
      return;
    }
    const { scope, provider } = editingForm;
    const trimmedBaseUrl = draftBaseUrl.trim();
    if (trimmedBaseUrl && !isHTTPURL(trimmedBaseUrl)) {
      dialogError = 'Base URL must use http:// or https://.';
      fieldErrors = {
        ...fieldErrors,
        [formKey(scope, provider)]: dialogError
      };
      return;
    }
    forms = forms.map((form) =>
      form.scope === scope && form.provider === provider
        ? {
            ...form,
            baseUrl: trimmedBaseUrl,
            apiKey: draftAPIKey.trim(),
            hasAPIKey: form.hasAPIKey || Boolean(draftAPIKey.trim()),
            source: form.source === 'override' || trimmedBaseUrl || draftAPIKey.trim() ? 'override' : form.source
          }
        : form
    );
    visibleProviders = {
      ...visibleProviders,
      [scope]: Array.from(new Set([...(visibleProviders[scope] ?? []), provider]))
    };
    const nextErrors = { ...fieldErrors };
    delete nextErrors[formKey(scope, provider)];
    fieldErrors = nextErrors;
    closeProviderDialog();
  }

  function openAddProviderDialog(scope: ProviderScope): void {
    const available = availableProviderItems(scope);
    if (available.length === 0) {
      return;
    }
    addProviderScope = scope;
    addProviderValue = available[0]?.value ?? '';
    addProviderDialogOpen = true;
  }

  function resetAddProviderDialog(): void {
    addProviderScope = null;
    addProviderValue = '';
  }

  function addVisibleProvider(): void {
    if (!addProviderScope || !addProviderValue) {
      return;
    }
    visibleProviders = {
      ...visibleProviders,
      [addProviderScope]: Array.from(new Set([...(visibleProviders[addProviderScope] ?? []), addProviderValue]))
    };
    addProviderDialogOpen = false;
    const form = formsForScope(addProviderScope).find((item) => item.provider === addProviderValue);
    if (form) {
      openProviderDialog(form);
    }
  }
</script>

<section class="page-stack">
  <PageHeader
    sticky
    eyebrow="Administer"
    title="Providers"
    description="Manage upstream provider credentials, base URLs, and default routes. Writes are audited on the target."
  >
    <div slot="actions">
      {#if $targetState.session.authenticated && !loading}
        <div class="inline-group">
          <Button class="btn-primary" type="button" disabled={saving} onclick={persist}>
            {saving ? 'Saving…' : 'Save provider settings'}
          </Button>
        </div>
      {/if}
    </div>
  </PageHeader>

  {#if !$targetState.session.authenticated}
    <div class="notice-card">Connect this target to manage provider settings.</div>
  {:else if error}
    <div class="error-state">{error}</div>
  {:else if loading}
    <div class="notice-card">Loading provider settings…</div>
  {:else}
    {#if success}
      <div class="notice-card is-success">{success}</div>
    {/if}

    <div class="providers-grid">
      {#each scopeOrder as typedScope}
        <section class="card page-stack provider-scope-card">
          <div class="provider-scope-header">
            <div class="page-header-meta">
              <h2 class="section-title">{scopeTitle(typedScope)}</h2>
            </div>

            <div class="provider-scope-actions">
              <div class="provider-route-control">
                <span class="provider-route-tag">Route</span>
                {#if scopeSupportsDefaultRoute(typedScope)}
                  {#if typedScope === 'gateway'}
                    <SelectField label={`${scopeTitle(typedScope)} default route`} hideLabel
                      bind:value={gatewayDefaultProvider} items={providerItemsForScope(typedScope)}
                      error={fieldErrors.gatewayDefault} placeholder="Select route" triggerClass="provider-route-trigger" />
                  {:else}
                    <SelectField label={`${scopeTitle(typedScope)} default route`} hideLabel
                      bind:value={internalDefaultProvider} items={providerItemsForScope(typedScope)}
                      error={fieldErrors.internalDefault} placeholder="Select route" triggerClass="provider-route-trigger" />
                  {/if}
                {:else}
                  <span class="provider-route-readout mono">{routeDisplayValue(typedScope)}</span>
                {/if}
              </div>

              <Button
                class="btn-secondary btn-sm"
                type="button"
                disabled={availableProviderItems(typedScope).length === 0}
                onclick={() => openAddProviderDialog(typedScope)}
              >
                <Plus size={13} strokeWidth={2} />
                Add
              </Button>
            </div>
          </div>

          {#if (typedScope === 'gateway' || typedScope === 'internal') && fieldErrors[typedScope === 'gateway' ? 'gatewayDefault' : 'internalDefault']}
            <div class="field-error">{fieldErrors[typedScope === 'gateway' ? 'gatewayDefault' : 'internalDefault']}</div>
          {/if}

          <div class="provider-list data-list">
            {#each visibleFormsForScope(typedScope) as form}
              {@const diagnostic = diagnosticFor(form.scope, form.provider)}
              {@const isActive = activeProviderForScope(typedScope) === form.provider}
              {@const issue = primaryIssue(form, diagnostic)}
              <div class="provider-list-item data-list-row" class:is-active={isActive}>
                <div class="provider-list-main">
                  <div class="provider-list-heading">
                    <strong>{form.provider}</strong>
                    {#if isActive}
                      <StatusBadge tone="brand" label="Active route" />
                    {/if}
                    <StatusBadge
                      tone={form.hasAPIKey ? 'success' : 'warning'}
                      label={form.hasAPIKey ? 'Key set' : 'No key'}
                    />
                    {#if form.source === 'override'}
                      <StatusBadge tone="muted" label="Override" />
                    {/if}
                  </div>
                  <div class="provider-list-endpoint mono">{providerEndpoint(form, diagnostic)}</div>
                  {#if issue}
                    <div class="provider-warning">
                      <TriangleAlert size={13} strokeWidth={1.75} />
                      <span>{issue}</span>
                    </div>
                  {/if}
                </div>
                <div class="provider-list-side">
                  <Button class="btn-ghost btn-icon btn-sm" type="button" aria-label={`Edit ${form.provider}`} onclick={() => openProviderDialog(form)}>
                    <PencilLine size={14} strokeWidth={1.75} />
                  </Button>
                </div>
              </div>
            {/each}
          </div>
        </section>
      {/each}
    </div>
  {/if}

  <Dialog.Root bind:open={providerDialogOpen}>
    <Dialog.Portal>
      <Dialog.Overlay class="dialog-overlay" />
      <Dialog.Content class="dialog-card provider-dialog-card">
        <div class="page-header">
          <div class="page-header-meta">
            <Dialog.Title class="section-title provider-dialog-title" level={3}>
              {editingForm ? `Edit ${editingForm.provider}` : 'Provider'}
            </Dialog.Title>
            <p class="panel-meta">Writes are audited on the selected target.</p>
          </div>
          <Dialog.Close class="btn-ghost btn-sm">Close</Dialog.Close>
        </div>

        {#if editingForm}
          {@const editingDiagnostic = diagnosticFor(editingForm.scope, editingForm.provider)}
          <div class="provider-dialog-summary">
            <span class="helper-text mono">{providerEndpoint(editingForm, editingDiagnostic)}</span>
            {#if editingDiagnostic?.restart_required}
              <StatusBadge tone="warning" label="Restart pending" />
            {/if}
          </div>

          <div class="form-grid two-up">
            <TextInputField
              label="Base URL"
              bind:value={draftBaseUrl}
              placeholder="https://api.example.com/v1"
              error={fieldErrors[formKey(editingForm.scope, editingForm.provider)]}
            />
            <TextInputField
              label="Rotate API key"
              bind:value={draftAPIKey}
              type="password"
              autocomplete="off"
              placeholder="Leave blank to keep the stored secret"
            />
          </div>

          {#if dialogError}
            <div class="error-state">{dialogError}</div>
          {/if}
          {#if (editingDiagnostic?.issues.length ?? 0) > 0}
            <div class="notice-card is-warning">{editingDiagnostic?.issues?.[0]}</div>
          {/if}

          <div class="target-dialog-footer">
            <p class="helper-text">Changes stage into the form first, then save to the target.</p>
            <Button class="btn-primary" type="button" onclick={applyDialogChanges}>Apply changes</Button>
          </div>
        {/if}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>

  <Dialog.Root bind:open={addProviderDialogOpen}>
    <Dialog.Portal>
      <Dialog.Overlay class="dialog-overlay" />
      <Dialog.Content class="dialog-card provider-add-dialog">
        <div class="page-header">
          <Dialog.Title class="section-title" level={3}>
            {addProviderScope ? `Add ${scopeTitle(addProviderScope)}` : 'Add provider'}
          </Dialog.Title>
          <Dialog.Close class="btn-ghost btn-sm">Close</Dialog.Close>
        </div>

        {#if addProviderScope}
          <SelectField
            label="Provider"
            bind:value={addProviderValue}
            items={availableProviderItems(addProviderScope)}
          />

          <div class="inline-group" style="justify-content: flex-end;">
            <Button class="btn-primary btn-sm" type="button" disabled={!addProviderValue} onclick={addVisibleProvider}>
              Add provider
            </Button>
          </div>
        {/if}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
</section>

<style>
  .providers-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px;
    align-items: start;
  }

  .provider-scope-card {
    gap: 14px;
    min-height: 100%;
  }

  .provider-scope-header { display: grid; gap: 16px; }

  .provider-scope-actions {
    display: flex;
    padding-top: 14px;
    border-top: 1px solid var(--border-subtle);
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .provider-route-control {
    flex: 1;
    min-width: 0;
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }

  .provider-route-tag {
    color: var(--text-muted);
    font-size: var(--font-size-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0;
  }

  :global(.provider-route-trigger) {
    min-width: 150px;
    max-width: none;
    flex: 1;
    height: var(--control-height);
    padding: 0 var(--control-padding-x);
  }

  .provider-route-readout {
    display: inline-flex;
    align-items: center;
    min-height: 28px;
    padding: 0 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
    color: var(--text);
    font-size: var(--font-size-sm);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 220px;
  }

  .provider-list {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }

  .provider-list-item {
    min-width: 0;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }

  .provider-list-item.is-active .provider-list-heading strong { color: var(--brand-strong); }

  .provider-list-main {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .provider-list-heading {
    overflow-wrap: anywhere;
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }

  .provider-list-endpoint {
    color: var(--text-secondary);
    font-size: var(--font-size-sm);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .provider-warning {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--warning);
    font-size: var(--font-size-xs);
  }

  .provider-list-side {
    flex-shrink: 0;
  }

  .provider-dialog-summary {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-muted);
  }

  :global(.provider-dialog-card) {
    display: grid;
    gap: 14px;
  }

  :global(.provider-dialog-title) {
    text-transform: capitalize;
  }

  :global(.provider-add-dialog) {
    max-width: 480px;
    display: grid;
    gap: 14px;
  }

  .target-dialog-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
  }

  .target-dialog-footer .helper-text {
    max-width: 320px;
  }

  @media (max-width: 900px) {
    .providers-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 640px) {
    .provider-scope-actions,
    .provider-route-control {
      width: 100%;
    }
    :global(.provider-route-trigger),
    .provider-route-readout {
      max-width: none;
      flex: 1 1 auto;
    }
    .target-dialog-footer {
      flex-direction: column;
      align-items: stretch;
    }
  }
</style>
