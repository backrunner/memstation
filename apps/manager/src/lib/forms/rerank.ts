export const rerankProviders = [
  { value: 'disabled', label: 'Disabled' },
  { value: 'cohere', label: 'Cohere' },
  { value: 'jina', label: 'Jina' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'rerank_compatible', label: 'Local / compatible rerank API' }
];

export function parseRerankModel(value: string): { provider: string; model: string } {
  if (!value.trim()) return { provider: 'disabled', model: '' };
  const index = value.indexOf(':');
  if (index >= 0) {
    const provider = value.slice(0,index).trim().toLowerCase();
    if (rerankProviders.some(item => item.value === provider && provider !== 'disabled')) return { provider, model:value.slice(index+1) };
  }
  return { provider: 'unknown', model: value };
}

export function providerLabel(value: string): string {
  return rerankProviders.find((item) => item.value === value)?.label ?? value;
}

export function rerankKeyOptional(value: string): boolean {
  return value === 'rerank_compatible';
}
