<script lang="ts">
  import SelectField from '#lib/components/ui/SelectField.svelte';
  import TextInputField from '#lib/components/ui/TextInputField.svelte';
  import { parseRerankModel, rerankProviders } from '#lib/forms/rerank.ts';

  let { value = $bindable(''), error = '' }: { value?: string; error?: string } = $props();
  const parsed = $derived(parseRerankModel(value));
  const items = $derived(parsed.provider === 'unknown'
    ? [...rerankProviders, { value: 'unknown', label: 'Unrecognized provider' }]
    : rerankProviders);
  const help = $derived(parsed.provider === 'openrouter'
      ? 'Enter the full OpenRouter model ID, including its vendor prefix.'
      : 'Enter the model ID accepted by this provider. ');

  function changeProvider(provider: string) {
    value = provider === 'disabled' ? '' : `${provider}:${parsed.provider === 'unknown' ? '' : parsed.model}`;
  }
</script>

<div class="rerank-model-fields">
  <SelectField label="Rerank provider" value={parsed.provider} {items} onValueChange={changeProvider} />
  <TextInputField
    label="Rerank model"
    value={parsed.model}
    disabled={parsed.provider === 'disabled'}
    placeholder={parsed.provider === 'openrouter' ? 'vendor/model-id' : 'Model ID'}
    help={parsed.provider === 'disabled' ? 'Enable a provider to rerank retrieved memories.' : help}
    {error}
    oninput={(event) => { value = parsed.provider === 'unknown' ? event.currentTarget.value : `${parsed.provider}:${event.currentTarget.value}`; }}
  />
  {#if parsed.provider !== 'disabled'}
    <a class="rerank-provider-link" href="/settings/providers">Configure provider endpoint and credentials</a>
  {/if}
</div>

<style>
  .rerank-model-fields { display: grid; grid-column: 1 / -1; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; }
  .rerank-provider-link { grid-column: 1 / -1; font-size: 0.875rem; width: fit-content; text-underline-offset: 3px; }
  @media (max-width: 640px) { .rerank-model-fields { grid-template-columns: minmax(0, 1fr); } }
</style>
