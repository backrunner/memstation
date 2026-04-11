import { expect, test, type Page } from '@playwright/test';

const fixtureBaseUrl = 'http://127.0.0.1:18080';
const adminToken = 'memstation-e2e-admin-token';
const gatewayAPIKey = 'ms-e2e';
const targetConfig = {
  id: 'fixture-target',
  label: 'Fixture Target',
  baseUrl: fixtureBaseUrl,
  environment: 'e2e',
  clusterName: 'standalone',
  region: 'local',
  kind: 'single'
};

async function resetFixture(page: Page): Promise<void> {
  const response = await page.context().request.post(`${fixtureBaseUrl}/__test/reset`);
  expect(response.ok()).toBeTruthy();
}

async function configureTarget(page: Page): Promise<void> {
  await page.addInitScript((target) => {
    window.localStorage.setItem('memstation.manager.targets', JSON.stringify([target]));
    window.localStorage.setItem('memstation.manager.selected-target', target.id);
  }, targetConfig);
}

async function login(page: Page): Promise<void> {
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: /Sign in to the selected MemStation target/i })).toBeVisible();
  await expect(page.getByText(`Target: ${targetConfig.label} · ${targetConfig.baseUrl}`)).toBeVisible();
  await page.getByPlaceholder('MEMSTATION_ADMIN_TOKEN').fill(adminToken);
  await page.getByRole('button', { name: 'Start admin session' }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: /Operational overview for/i })).toBeVisible();
}

async function expectRouteHealthy(page: Page, path: string, heading: RegExp): Promise<void> {
  await page.goto(path);
  await expect(page.getByRole('heading', { name: heading }).first()).toBeVisible();
  await expect(page.locator('.error-state')).toHaveCount(0);
}

async function toggleSwitch(page: Page, label: string): Promise<void> {
  const row = page.locator('.switch-row').filter({ hasText: label }).first();
  await expect(row).toBeVisible();
  await row.locator('[role="switch"]').click();
}

async function chooseBitsSelectOption(page: Page, fieldLabel: string, optionLabel: string): Promise<void> {
  const field = page.locator('label.field').filter({ hasText: fieldLabel }).first();
  await expect(field).toBeVisible();
  await field.locator('.select-trigger').click();
  await page.locator('.select-item').filter({ hasText: optionLabel }).click();
}

test.beforeEach(async ({ page }) => {
  await resetFixture(page);
  await configureTarget(page);
  await login(page);
});

test('dashboard and major workspaces load live target data', async ({ page }) => {
  await expect(page.getByText('Stored Memories')).toBeVisible();
  await expect(page.getByText('default').first()).toBeVisible();
  await expect(page.getByText('memory_document_create').first()).toBeVisible();

  await expectRouteHealthy(page, '/users', /Track every user footprint/i);
  await expect(page.getByText('Admin User').first()).toBeVisible();

  await expectRouteHealthy(page, '/users/admin', /^admin$/i);
  await expect(page.getByText('Playground key').first()).toBeVisible();

  await expectRouteHealthy(page, '/conversations', /Trace each request chain/i);
  await expect(page.getByText('conv-seed-1').first()).toBeVisible();

  await expectRouteHealthy(page, '/memory', /Inspect retrieval quality/i);
  await expect(page.getByText('Launch checklist').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/health', /Live and readiness signals/i);
  await expect(page.getByText('postgres').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/cache', /L1\/L2 cache posture/i);
  await expect(page.getByText('Pub/Sub active').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/queues/default', /^default$/i);
  await expect(page.getByText('conversation.finalize').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/tasks/memory/task-2', /^task-2$/i);
  await expect(page.getByText('fixture analysis timeout').first()).toBeVisible();

  await expectRouteHealthy(page, '/statistics/tokens', /Token and workload analysis/i);
  await expect(page.getByText('Filtered Tokens').first()).toBeVisible();

  await expectRouteHealthy(page, '/settings/providers', /Manage gateway, internal, and embedding provider sets/i);
  await expect(page.getByText('openai_compatible').first()).toBeVisible();

  await expectRouteHealthy(page, '/playground/protocols', /Inspect raw protocol compatibility/i);
  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByRole('button', { name: 'Send raw request' }).click();
  await expect(page.getByText('HTTP 200').first()).toBeVisible();
  await expect(page.getByText('streamed').first()).toBeVisible();
});

test('memory summary edits persist and update the audit trail', async ({ page }) => {
  const updatedContent = 'Updated summary from Playwright audit validation.';

  await page.goto('/memory/summaries');
  await expect(page.getByRole('heading', { name: /Review and correct conversation summaries/i })).toBeVisible();

  const summaryCard = page.locator('.debug-block').filter({ hasText: 'summary-1' }).first();
  await summaryCard.getByRole('link', { name: 'Open' }).click();

  await expect(page.getByRole('heading', { name: /^summary-1$/i })).toBeVisible();
  const contentField = page.locator('textarea').first();
  await contentField.fill(updatedContent);
  await page.getByRole('button', { name: 'Save changes' }).click();

  await expect(page.getByText('Summary updated and reloaded from the target.')).toBeVisible();
  await expect(page.getByText('memory_document_update')).toBeVisible();
  await expect(page.getByText(updatedContent)).toBeVisible();

  await page.reload();
  await expect(contentField).toHaveValue(updatedContent);
});

test('key memory CRUD works end to end from the manager', async ({ page }) => {
  const keyName = 'playwright_memory_slot';
  const initialValue = 'Initial operator memory from Playwright.';
  const updatedValue = 'Updated operator memory from Playwright.';

  await page.goto('/memory/keys');
  await expect(page.getByRole('heading', { name: /Manage stable key-value memory slots/i })).toBeVisible();

  await page.getByPlaceholder('preferred_name or recent_conversation').fill(keyName);
  await page.getByPlaceholder('profile, workflow, system').fill('workflow');
  await page.locator('textarea').first().fill(initialValue);
  await page.getByRole('button', { name: 'Create key memory' }).click();

  await expect(page.getByText('Key memory created and audited on the target.')).toBeVisible();
  const keyCard = page.locator('.debug-block').filter({ hasText: keyName }).first();
  await expect(keyCard).toBeVisible();

  await keyCard.getByRole('button', { name: 'Edit' }).click();
  await keyCard.locator('textarea').fill(updatedValue);
  await keyCard.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Key memory updated and audited on the target.')).toBeVisible();
  await expect(keyCard.getByText(updatedValue)).toBeVisible();

  await keyCard.getByRole('button', { name: 'Delete' }).click();
  await expect(page.locator('.debug-block').filter({ hasText: keyName })).toHaveCount(0);
});

test('user API key lifecycle and task rerun flows are operable', async ({ page }) => {
  await page.goto('/users/admin');
  await expect(page.getByRole('heading', { name: /^admin$/i })).toBeVisible();

  await page.getByPlaceholder('build-bot or local playground').fill('playwright-managed-key');
  await page.getByRole('button', { name: 'Create API key' }).click();
  await expect(page.getByText('API key created. The raw key is shown once below.')).toBeVisible();

  const apiKeyCard = page.locator('.debug-block').filter({ hasText: 'playwright-managed-key' }).first();
  await expect(apiKeyCard).toBeVisible();
  await apiKeyCard.getByRole('button', { name: 'Revoke key' }).click();
  await expect(apiKeyCard.getByText('Revoked')).toBeVisible();

  await page.goto('/operations/tasks/memory/task-2');
  await expect(page.getByRole('heading', { name: /^task-2$/i })).toBeVisible();
  await page.getByRole('button', { name: 'Run task now' }).click();
  await expect(page.getByText('1 reruns')).toBeVisible();
  await expect(page.getByText('requeue_task').first()).toBeVisible();
});

test('playground preview flow streams and shows preview diff output', async ({ page }) => {
  const prompt = 'Preview memory validation';

  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /Drive the gateway directly/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  await page.getByPlaceholder('Ask the target to recall memory, test a tool, or verify persistence behavior.').fill(prompt);
  await page.getByRole('button', { name: /^Send$/ }).click();

  await expect(page.getByText(`Fixture stream response for: ${prompt}`)).toBeVisible();
  await page.getByRole('button', { name: 'Thinking trace' }).click();
  await expect(page.getByText('Reviewing retrieved fixture memory before composing the answer.').first()).toBeVisible();
  await expect(page.getByText('Matched keywords').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Extracted' }).click();
  await expect(page.getByText(`Preview summary for: ${prompt}`).first()).toBeVisible();
  await expect(page.getByText('Keyword source').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Diff' }).click();
  await expect(page.getByText('Preview flow')).toBeVisible();
  await expect(page.getByText('validation_mode').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Raw Debug' }).click();
  await expect(page.getByText('Latest usage: 18 in / 14 out / 32 total')).toBeVisible();
});

test('playground persisted flow writes memory and is inspectable in admin views', async ({ page }) => {
  const prompt = 'Persist memory validation';

  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /Drive the gateway directly/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  const sessionId = await page.getByPlaceholder('playground-123').inputValue();

  await toggleSwitch(page, 'Memory Persist');
  await expect(page.getByText('Memory Persist On')).toBeVisible();

  await page.getByPlaceholder('Ask the target to recall memory, test a tool, or verify persistence behavior.').fill(prompt);
  await page.getByRole('button', { name: 'Send and End' }).click();

  await expect(page.getByText(`Fixture stream response for: ${prompt}`)).toBeVisible();
  await page.getByRole('tab', { name: 'Extracted' }).click();
  await expect(page.getByText(`Persisted summary for: ${prompt}`).first()).toBeVisible();
  await expect(page.getByText('Keyword source').first()).toBeVisible();
  await expect(page.getByText('last_validation_target').first()).toBeVisible();

  await page.goto(`/conversations/${encodeURIComponent(sessionId)}`);
  await expect(page.getByRole('heading', { name: new RegExp(`^${sessionId}$`) })).toBeVisible();
  await expect(page.getByText(`Persisted summary for: ${prompt}`)).toBeVisible();
  await expect(page.getByText('summary completed')).toBeVisible();
  await expect(page.getByText('analysis completed')).toBeVisible();
});

test('system runtime settings persist direct target overrides and can reset to defaults', async ({ page }) => {
  await page.goto('/settings/system');
  await expect(page.getByRole('heading', { name: /Control deployment-level runtime policy/i })).toBeVisible();

  await page.getByLabel('Provider timeout (seconds)').fill('75');
  await page.getByLabel('Cache TTL (seconds)').fill('333');
  await page.getByRole('button', { name: 'Save system settings' }).click();

  await expect(page.getByText(/require a target restart/i)).toBeVisible();
  await expect(page.getByText('Restart staged').first()).toBeVisible();
  await expect(page.getByText('Live applied').first()).toBeVisible();

  await page.reload();
  await expect(page.getByLabel('Provider timeout (seconds)')).toHaveValue('75');
  await expect(page.getByLabel('Cache TTL (seconds)')).toHaveValue('333');

  await page.getByRole('button', { name: 'Reset all overrides' }).click();
  await expect(page.getByText('All system runtime overrides were cleared back to deployment defaults.')).toBeVisible();
  await expect(page.getByLabel('Provider timeout (seconds)')).toHaveValue('60');
  await expect(page.getByLabel('Cache TTL (seconds)')).toHaveValue('120');
});

test('model and provider settings persist through direct writes to the selected target', async ({ page }) => {
  await page.goto('/settings/models');
  await expect(page.getByRole('heading', { name: /Models, thresholds, and provider overrides/i })).toBeVisible();

  await page.getByLabel('Chat model').fill('gpt-4.1-mini-playwright');
  await page.getByLabel('Embedding model').fill('text-embedding-3-large-playwright');
  await page.getByRole('button', { name: 'Enabled' }).first().click();
  await page.getByLabel('Max injection tokens').fill('4096');
  await page.getByRole('button', { name: 'Save settings' }).click();

  await expect(page.getByText('Configuration saved to the selected target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Chat model')).toHaveValue('gpt-4.1-mini-playwright');
  await expect(page.getByLabel('Embedding model')).toHaveValue('text-embedding-3-large-playwright');
  await expect(page.getByRole('button', { name: 'Enabled' }).first()).toHaveClass(/button-secondary/);
  await expect(page.getByLabel('Max injection tokens')).toHaveValue('4096');

  await page.goto('/settings/providers');
  await expect(page.getByRole('heading', { name: /Manage gateway, internal, and embedding provider sets/i })).toBeVisible();

  const gatewayProviderSection = page.locator('section.card').filter({ hasText: 'Gateway proxy providers' }).first();
  const anthropicCard = gatewayProviderSection
    .locator('section.card')
    .filter({ has: page.getByRole('heading', { name: 'anthropic' }) })
    .first();
  await anthropicCard.getByLabel('Base URL').fill('https://api.anthropic.com/v2/fixture');
  await anthropicCard.getByLabel('Rotate API key').fill('sk-playwright-anthropic');
  await page.getByRole('button', { name: 'Save provider set overrides' }).click();

  await expect(page.getByText('Provider set overrides saved. Sensitive API keys are not echoed back by the target.')).toBeVisible();
  await expect(anthropicCard.getByLabel('Base URL')).toHaveValue('https://api.anthropic.com/v2/fixture');
});

test('memory workspaces connect ranked recall, memory detail, and source conversation requeue flows', async ({ page }) => {
  await page.goto('/memory');
  await expect(page.getByRole('heading', { name: /Inspect retrieval quality/i })).toBeVisible();

  await page.getByLabel('Search query').fill('gateway memory');
  await page.getByRole('button', { name: 'Run semantic search' }).click();
  await expect(page.getByText('Query embedding token estimate: 12')).toBeVisible();
  const summaryResult = page.locator('.debug-block').filter({ hasText: 'MemStation launch tracks gateway rollout' }).first();
  await expect(summaryResult).toBeVisible();
  await expect(summaryResult.getByText('Matched keywords')).toBeVisible();
  await summaryResult.getByRole('link', { name: 'Open' }).click();

  await expect(page.getByRole('heading', { name: /^summary-1$/i })).toBeVisible();
  await expect(page.getByText('memory_document_create').first()).toBeVisible();
  await page.getByRole('link', { name: 'Open source conversation' }).click();

  await expect(page.getByRole('heading', { name: /^conv-seed-1$/i })).toBeVisible();
  await page.getByRole('button', { name: 'Requeue analysis' }).click();
  await expect(page.getByText('Analysis task requeued on the selected target.')).toBeVisible();
  await expect(page.locator('.message-body').filter({ hasText: 'MemStation launch tracks gateway rollout, memory verification, and admin observability.' }).first()).toBeVisible();

  await page.goto('/playground/memory');
  await expect(page.getByRole('heading', { name: /Validate extraction output and recall ranking/i })).toBeVisible();
  await page.getByLabel('Conversation ID').fill('conv-seed-1');
  await page.getByRole('button', { name: 'Inspect conversation' }).click();
  await expect(page.getByText('summary completed')).toBeVisible();
  await expect(page.getByText('analysis completed')).toBeVisible();
  await expect(page.getByText('Gateway smoke coverage is required before rollout.')).toBeVisible();
  await page.getByLabel('Memory search query').fill('gateway rollout');
  await page.getByRole('button', { name: 'Run recall search' }).click();
  await expect(page.getByText(/Query embedding tokens:/)).toBeVisible();
  await expect(page.getByText('MemStation launch tracks gateway rollout, memory verification, and admin observability.').first()).toBeVisible();
  await expect(page.getByText('Keyword source').first()).toBeVisible();
});

test('user settings writes are visible in audit filters', async ({ page }) => {
  await page.goto('/users/admin');
  await expect(page.getByRole('heading', { name: /^admin$/i })).toBeVisible();

  await page.getByLabel('Chat model').fill('gpt-4.1-user-playwright');
  await page.getByLabel('Summary model').fill('gpt-4.1-summary-playwright');
  await page.getByRole('button', { name: 'Enabled' }).first().click();
  await page.getByLabel('Max injection tokens').fill('3072');
  await page.getByRole('button', { name: 'Save user settings' }).click();

  await expect(page.getByText('User settings saved directly to the selected target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Chat model')).toHaveValue('gpt-4.1-user-playwright');
  await expect(page.getByLabel('Summary model')).toHaveValue('gpt-4.1-summary-playwright');
  await expect(page.getByLabel('Max injection tokens')).toHaveValue('3072');

  await page.goto('/audit?action=user_settings_update&target_type=user&target_id=admin');
  await expect(page.getByRole('heading', { name: /Sensitive action history/i })).toBeVisible();
  await expect(page.getByText('user_settings_update').first()).toBeVisible();
  await expect(page.getByText('gpt-4.1-user-playwright').first()).toBeVisible();
});

test('protocol playground handles responses streaming and anthropic message payloads', async ({ page }) => {
  await page.goto('/playground/protocols');
  await expect(page.getByRole('heading', { name: /Inspect raw protocol compatibility/i })).toBeVisible();

  await page.getByLabel('API key').fill(gatewayAPIKey);

  await chooseBitsSelectOption(page, 'Protocol', 'OpenAI Responses');
  await page.getByRole('button', { name: 'Send raw request' }).click();
  await expect(page.getByText('HTTP 200').first()).toBeVisible();
  await expect(page.getByText('response.reasoning.delta').first()).toBeVisible();
  await expect(page.getByText('response.output_text.delta').first()).toBeVisible();

  await chooseBitsSelectOption(page, 'Protocol', 'Anthropic Messages');
  await page.locator('textarea.textarea').fill(
    JSON.stringify(
      {
        model: 'configured-anthropic-model',
        stream: false,
        messages: [{ role: 'user', content: 'Explain anthropic response rendering.' }]
      },
      null,
      2
    )
  );
  await page.getByRole('button', { name: 'Send raw request' }).click();
  await expect(page.getByText('HTTP 200').first()).toBeVisible();
  await expect(page.getByText('"type": "message"').first()).toBeVisible();
  await expect(page.getByText('Fixture stream response for: Explain anthropic response rendering.')).toBeVisible();
});
