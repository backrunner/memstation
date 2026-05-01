import { expect, test, type Page } from '@playwright/test';

const fixtureBaseUrl = 'http://127.0.0.1:18080';
const adminToken = 'memstation-e2e-admin-token';
const adminTokenHash =
  '$memstation-admin-argon2id$v=19$m=65536,t=1,p=4$bWVtc3RhdGlvbi1zYWx0MQ$ITiBXhJ8JgTOQi90bJjQHFgRrs+aA9nOPRW9yfT31N4';
const gatewayAPIKey = 'ms-e2e';
const targetConfig = {
  id: 'fixture-target',
  label: 'Fixture Target',
  baseUrl: fixtureBaseUrl,
  environment: 'custom',
  clusterName: 'standalone',
  region: 'global',
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
  await expect(page.getByRole('heading', { name: /^Connect an instance$/i })).toBeVisible();
  await page.getByLabel('Connection URI or base URL').fill(targetConfig.baseUrl);
  await page.getByLabel('Label').fill(targetConfig.label);
  await page.getByPlaceholder('MEMSTATION_ADMIN_TOKEN').fill(adminToken);
  await page.getByRole('button', { name: 'Connect target' }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: /^Dashboard$/i })).toBeVisible();
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
  const field = page.locator('.field').filter({ hasText: fieldLabel }).first();
  await expect(field).toBeVisible();
  await field.locator('.select-trigger').click();
  await page.locator('.select-item').filter({ hasText: optionLabel }).click();
}

async function ensureProviderRowVisible(page: Page, sectionLabel: RegExp, providerName: string) {
  const section = page.locator('section.card').filter({ hasText: sectionLabel }).first();
  const row = section.locator('.provider-list-item').filter({ hasText: new RegExp(providerName, 'i') }).first();
  if (await row.count()) {
    return { section, row, openedDialog: false };
  }

  await section.locator('.provider-add-button').click();
  const addDialog = page.locator('.provider-add-dialog');
  await expect(addDialog).toBeVisible();
  await addDialog.locator('.select-trigger').click();
  await page.locator('.select-item').filter({ hasText: new RegExp(`^${providerName}$`, 'i') }).click();
  await addDialog.getByRole('button', { name: 'Add Provider' }).click();
  await expect(page.locator('.provider-dialog-card')).toBeVisible();
  return { section, row, openedDialog: true };
}

test.beforeEach(async ({ page }) => {
  await resetFixture(page);
  await configureTarget(page);
  await login(page);
});

test('target manager accepts URI tokens without storing them with targets', async ({ page }) => {
  await page.context().clearCookies();
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: /^Connect an instance$/i })).toBeVisible();
  await expect(page.getByText('The admin token is exchanged once for a secure server session.')).toBeVisible();
  await expect(page.getByText('Target initialization')).toHaveCount(0);
  await expect(page.locator('.target-preview')).toHaveCount(0);

  await page.getByLabel('Connection URI or base URL').fill(
    `memstation://127.0.0.1:18080?key=${encodeURIComponent(adminTokenHash)}&label=URI%20Target`
  );
  await expect(page.getByLabel('Admin token')).toHaveCount(0);
  await page.getByRole('button', { name: 'Connect target' }).click();

  await expect(page.getByRole('button', { name: 'Switch target' })).toContainText('127.0.0.1:18080');
  await page.getByRole('button', { name: 'Switch target' }).click();
  await expect(page.locator('.target-selector-item').filter({ hasText: '127.0.0.1:18080' })).toBeVisible();
  await expect(page.locator('.target-selector-item').filter({ hasText: 'fixture' })).toBeVisible();
  await page.keyboard.press('Escape');

  const savedTargets = await page.evaluate(() => window.localStorage.getItem('memstation.manager.targets') ?? '');
  expect(savedTargets).toContain('URI Target');
  expect(savedTargets).not.toContain(adminToken);
  expect(savedTargets).not.toContain(adminTokenHash);

  const session = await page.evaluate(async (baseUrl) => {
    const response = await fetch(`${baseUrl}/api/admin/session`, { credentials: 'include' });
    return (await response.json()) as { authenticated: boolean };
  }, fixtureBaseUrl);
  expect(session.authenticated).toBe(true);
});

test('connect page requires an admin token before saving the first target', async ({ page }) => {
  await page.evaluate(() => {
    window.localStorage.removeItem('memstation.manager.targets');
    window.localStorage.removeItem('memstation.manager.selected-target');
  });
  await page.goto('/login');
  await page.getByLabel('Connection URI or base URL').fill(fixtureBaseUrl);
  await page.getByLabel('Label').fill('No Token Target');
  await page.getByRole('button', { name: 'Connect target' }).click();
  await expect(page.getByText('Admin token is required. A target cannot be connected without it.')).toBeVisible();

  const savedTargets = await page.evaluate(() => window.localStorage.getItem('memstation.manager.targets'));
  expect(savedTargets).toBeNull();
});

test('target manager removes saved targets from the row context menu after confirmation', async ({ page }) => {
  const removableTargetLabel = 'Removable Target';
  const removableTargetUrl = 'http://127.0.0.1:18081';

  await page.getByRole('button', { name: 'Targets' }).click();
  const targetDialog = page.getByRole('dialog', { name: 'Target connections' });
  await expect(targetDialog).toBeVisible();
  await expect(targetDialog.getByRole('button', { name: /^Remove$/i })).toHaveCount(0);

  await targetDialog.getByRole('button', { name: 'New target' }).click();
  const draftRow = targetDialog.locator('.target-row.is-draft').first();
  await expect(draftRow).toBeVisible();
  await expect(draftRow).toContainText('New target');
  await targetDialog.getByLabel('Connection URI or base URL').fill(removableTargetUrl);
  await targetDialog.getByLabel('Label').fill(removableTargetLabel);
  await expect(draftRow).toContainText(removableTargetLabel);
  await expect(draftRow).toContainText(removableTargetUrl);
  await targetDialog.getByRole('button', { name: 'Save target' }).click();
  await expect(targetDialog).toBeHidden();

  await page.getByRole('button', { name: 'Targets' }).click();
  await expect(targetDialog).toBeVisible();
  const removableRow = targetDialog.locator('.target-row').filter({ hasText: removableTargetLabel }).first();
  await expect(removableRow).toBeVisible();
  await removableRow.click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Delete target' }).click();

  const removeDialog = page.getByRole('alertdialog', { name: 'Delete target?' });
  await expect(removeDialog).toBeVisible();
  await expect(removeDialog.getByText(removableTargetUrl)).toBeVisible();
  await removeDialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(removeDialog).toBeHidden();
  await expect(removableRow).toBeVisible();

  await removableRow.click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Delete target' }).click();
  await removeDialog.getByRole('button', { name: 'Delete target' }).click();
  await expect(targetDialog.locator('.target-row').filter({ hasText: removableTargetLabel })).toHaveCount(0);
  await expect(targetDialog.locator('.target-row').filter({ hasText: targetConfig.label })).toBeVisible();
});

test('dashboard and major workspaces load live target data', async ({ page }) => {
  await expect(page.getByText('Stored Memories')).toBeVisible();
  await expect(page.getByText('default').first()).toBeVisible();
  await expect(page.getByText('memory_document_create').first()).toBeVisible();

  await expectRouteHealthy(page, '/users', /^Users$/i);
  await expect(page.getByText('Admin User').first()).toBeVisible();

  await expectRouteHealthy(page, '/users/admin', /^admin$/i);
  await expect(page.getByText('Playground key').first()).toBeVisible();

  await expectRouteHealthy(page, '/conversations', /^Conversations$/i);
  await expect(page.getByText('conv-seed-1').first()).toBeVisible();

  await expectRouteHealthy(page, '/memory', /^Memory Workspace$/i);
  await expect(page.getByText('Launch checklist').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/health', /^Health$/i);
  await expect(page.getByText('postgres').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/cache', /^Cache$/i);
  await expect(page.getByText('Pub/Sub active').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/queues/default', /^default$/i);
  await expect(page.getByText('conversation.finalize').first()).toBeVisible();

  await expectRouteHealthy(page, '/operations/tasks/memory/task-2', /^task-2$/i);
  await expect(page.getByText('fixture analysis timeout').first()).toBeVisible();

  await expectRouteHealthy(page, '/statistics/tokens', /Token usage/i);
  await expect(page.getByText('Filtered Tokens').first()).toBeVisible();

  await expectRouteHealthy(page, '/settings/providers', /^Providers$/i);
  await expect(page.getByText('openai_compatible').first()).toBeVisible();

  await expectRouteHealthy(page, '/playground/protocols', /^Protocols$/i);
  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByRole('button', { name: 'Send request' }).click();
  await expect(page.getByText('streamed').first()).toBeVisible();
});

test('memory summary edits persist and update the audit trail', async ({ page }) => {
  const updatedContent = 'Updated summary from Playwright audit validation.';

  await page.goto('/memory/summaries');
  await expect(page.getByRole('heading', { name: /^Summaries$/i })).toBeVisible();

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
  await expect(page.getByRole('heading', { name: /^Key Memories$/i })).toBeVisible();

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
  await expect(page.getByRole('heading', { name: /^Chat Playground$/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(prompt);
  await page.getByRole('button', { name: /^Send$/ }).click();

  await expect(page.getByText(`Fixture stream response for: ${prompt}`)).toBeVisible();
  await page.getByRole('button', { name: 'Thinking trace' }).click();
  await expect(page.getByText('Reviewing retrieved fixture memory before composing the answer.').first()).toBeVisible();
  await expect(page.getByText('Matched keywords').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Recall' }).click();
  await expect(page.getByText(`Preview summary for: ${prompt}`).first()).toBeVisible();
  await expect(page.getByText('Keyword source').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Diff' }).click();
  await expect(page.getByText('Preview flow')).toBeVisible();
  await expect(page.getByText('validation_mode').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Raw' }).click();
  await expect(page.getByText('18 in / 14 out / 32 total')).toBeVisible();
});

test('playground persisted flow writes memory and is inspectable in admin views', async ({ page }) => {
  const prompt = 'Persist memory validation';

  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /^Chat Playground$/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  const sessionId = await page.getByPlaceholder('playground-123').inputValue();

  await toggleSwitch(page, 'Memory persist');
  await expect(page.locator('.switch-row').filter({ hasText: 'Memory persist' }).locator('[role="switch"]').first()).toHaveAttribute('data-state', 'checked');

  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(prompt);
  await page.getByRole('button', { name: 'Send and End' }).click();

  await expect(page.getByText(`Fixture stream response for: ${prompt}`)).toBeVisible();
  await page.getByRole('tab', { name: 'Recall' }).click();
  await expect(page.getByText(`Persisted summary for: ${prompt}`).first()).toBeVisible();
  await expect(page.getByText('Keyword source').first()).toBeVisible();
  await expect(page.getByText('last_validation_target').first()).toBeVisible();

  await page.goto(`/conversations/${encodeURIComponent(sessionId)}`);
  await expect(page.getByRole('heading', { name: new RegExp(`^${sessionId}$`) })).toBeVisible();
  await expect(page.getByText(`Persisted summary for: ${prompt}`)).toBeVisible();
  await expect(page.getByText('summary completed')).toBeVisible();
  await expect(page.getByText('analysis completed')).toBeVisible();
});

test('playground conversation switching keeps background turns running and failed turns out of generating', async ({ page }) => {
  const slowPrompt = 'slow background completion';
  const rechargePrompt = 'recharge required for switch';

  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /^Chat Playground$/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(slowPrompt);
  await page.getByRole('button', { name: /^Send$/ }).click();

  await expect(page.getByRole('button', { name: 'Generating...' })).toBeVisible();
  await page.getByRole('button', { name: 'New thread' }).click();

  await expect(page.getByPlaceholder('Configured model name')).toHaveValue('gpt-4.1-mini');
  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(rechargePrompt);
  await page.getByRole('button', { name: /^Send$/ }).click();

  await expect(page.locator('.field-error').filter({ hasText: 'Please recharge your balance before continuing this conversation.' })).toBeVisible();
  await expect(page.getByRole('button', { name: /^Send$/ })).toBeEnabled();

  await page.getByRole('tab', { name: new RegExp(slowPrompt, 'i') }).click();
  await expect(page.getByText(`Fixture stream response for: ${slowPrompt}`)).toBeVisible();

  await page.getByRole('tab', { name: new RegExp(rechargePrompt, 'i') }).click();
  await expect(page.locator('.field-error').filter({ hasText: 'Please recharge your balance before continuing this conversation.' })).toBeVisible();
  await expect(page.getByRole('button', { name: /^Send$/ })).toBeEnabled();
});

test('playground and settings drafts survive internal route changes', async ({ page }) => {
  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /^Chat Playground$/i })).toBeVisible();
  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  await toggleSwitch(page, 'Memory persist');
  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill('Draft preservation turn');
  await page.getByRole('button', { name: /^Send$/ }).click();
  await expect(page.getByText('Fixture stream response for: Draft preservation turn')).toBeVisible();

  await page.goto('/dashboard');
  await page.goto('/playground/chat');
  await expect(page.getByText('Fixture stream response for: Draft preservation turn')).toBeVisible();
  await expect(page.getByPlaceholder('Configured model name')).toHaveValue('gpt-4.1-mini');
  await expect(page.locator('.switch-row').filter({ hasText: 'Memory persist' }).locator('[role="switch"]').first()).toHaveAttribute('data-state', 'checked');

  await page.goto('/playground/protocols');
  await expect(page.getByRole('heading', { name: /^Protocols$/i })).toBeVisible();
  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.locator('.field').filter({ hasText: 'Provider override' }).first().locator('input').fill('openai');
  await page.locator('textarea').first().fill(JSON.stringify({ model: 'draft-model', stream: false, messages: [{ role: 'user', content: 'Draft route retention' }] }, null, 2));

  await page.goto('/dashboard');
  await page.goto('/playground/protocols');
  await expect(page.locator('.field').filter({ hasText: 'Provider override' }).first().locator('input')).toHaveValue('openai');
  await expect(page.locator('textarea').first()).toHaveValue(/Draft route retention/);

  await page.goto('/playground/memory');
  await expect(page.getByRole('heading', { name: /^Memory Playground$/i })).toBeVisible();
  await page.locator('.field').filter({ hasText: 'Conversation ID' }).first().locator('input').fill('conv-seed-1');
  await page.locator('.field').filter({ hasText: 'Memory search query' }).first().locator('input').fill('launch checklist');

  await page.goto('/dashboard');
  await page.goto('/playground/memory');
  await expect(page.locator('.field').filter({ hasText: 'Conversation ID' }).first().locator('input')).toHaveValue('conv-seed-1');
  await expect(page.locator('.field').filter({ hasText: 'Memory search query' }).first().locator('input')).toHaveValue('launch checklist');

  await page.goto('/settings/models');
  await expect(page.getByRole('heading', { name: /^Models$/i })).toBeVisible();
  await page.locator('.field').filter({ hasText: 'Chat Model' }).first().locator('input').fill('draft-chat-model');
  await toggleSwitch(page, 'Tool Writes');

  await page.goto('/dashboard');
  await page.goto('/settings/models');
  await expect(page.locator('.field').filter({ hasText: 'Chat Model' }).first().locator('input')).toHaveValue('draft-chat-model');
  await expect(page.locator('.switch-row').filter({ hasText: 'Tool Writes' }).locator('[role="switch"]').first()).toHaveAttribute('data-state', 'checked');

  await page.goto('/settings/system');
  await expect(page.getByRole('heading', { name: /^System$/i })).toBeVisible();
  await page.locator('.field').filter({ hasText: 'Rerank Model' }).first().locator('input').fill('cohere:rerank-v3.5');
  await page.locator('.field').filter({ hasText: 'Connection URI' }).first().locator('input').fill('postgres://draft:secret@db.example.com:5432/memstation');

  await page.goto('/dashboard');
  await page.goto('/settings/system');
  await expect(page.locator('.field').filter({ hasText: 'Rerank Model' }).first().locator('input')).toHaveValue('cohere:rerank-v3.5');
  await expect(page.locator('.field').filter({ hasText: 'Connection URI' }).first().locator('input')).toHaveValue('postgres://draft:secret@db.example.com:5432/memstation');
});

test('admin editing drafts survive route changes across providers, users, and key memories', async ({ page }) => {
  await page.goto('/settings/providers');
  await expect(page.getByRole('heading', { name: /^Providers$/i })).toBeVisible();

  const providerDialog = page.locator('.provider-dialog-card');
  const gatewayProviderSection = page.locator('section.card').filter({ hasText: /Gateway Providers/i }).first();
  const anthropicRow = gatewayProviderSection.locator('.provider-list-item').filter({ hasText: /anthropic/i }).first();
  await anthropicRow.getByRole('button', { name: /Edit anthropic/i }).click();
  await expect(providerDialog).toBeVisible();
  await providerDialog.getByLabel('Base URL').fill('https://draft.anthropic.internal/v1');
  await providerDialog.getByLabel('Rotate API Key').fill('sk-draft-provider');

  await page.goto('/dashboard');
  await page.goto('/settings/providers');
  await expect(providerDialog).toBeVisible();
  await expect(providerDialog.getByLabel('Base URL')).toHaveValue('https://draft.anthropic.internal/v1');
  await expect(providerDialog.getByLabel('Rotate API Key')).toHaveValue('sk-draft-provider');
  await providerDialog.getByRole('button', { name: 'Close' }).click();

  await page.goto('/users/admin');
  await expect(page.getByRole('heading', { name: /^admin$/i })).toBeVisible();
  await page.getByLabel('Chat model').fill('gpt-4.1-user-draft');
  await page.getByPlaceholder('build-bot or local playground').fill('draft-key-label');

  await page.goto('/dashboard');
  await page.goto('/users/admin');
  await expect(page.getByLabel('Chat model')).toHaveValue('gpt-4.1-user-draft');
  await expect(page.getByPlaceholder('build-bot or local playground')).toHaveValue('draft-key-label');

  await page.goto('/memory/keys');
  await expect(page.getByRole('heading', { name: /^Key Memories$/i })).toBeVisible();
  await page.getByLabel('Filter key memories').fill('validation');
  await page.getByPlaceholder('preferred_name or recent_conversation').fill('draft_slot');
  await page.getByPlaceholder('profile, workflow, system').fill('workflow');
  await page.locator('textarea').first().fill('Draft key memory value');

  await page.goto('/dashboard');
  await page.goto('/memory/keys');
  await expect(page.getByLabel('Filter key memories')).toHaveValue('validation');
  await expect(page.getByPlaceholder('preferred_name or recent_conversation')).toHaveValue('draft_slot');
  await expect(page.getByPlaceholder('profile, workflow, system')).toHaveValue('workflow');
  await expect(page.locator('textarea').first()).toHaveValue('Draft key memory value');
});

test('conversation detail renders multimodal tool payloads from raw messages and timeline parts', async ({ page }) => {
  await page.goto('/conversations/conv-mm-tool-1');
  await expect(page.getByRole('heading', { name: /^conv-mm-tool-1$/i })).toBeVisible();

  await page.getByRole('button', { name: 'Tool calls (1)' }).click();
  await expect(page.getByText('Tool input media · tool_input.attachments[0]')).toBeVisible();
  await expect(page.getByAltText('diagram-tool.png').first()).toBeVisible();
  await expect(page.getByText('tool-brief.pdf').first()).toBeVisible();

  await page.getByRole('button', { name: 'Tool results (1)' }).click();
  await expect(page.getByText('Tool output media · tool_output.content[1]')).toBeVisible();
  await expect(page.getByText('audio transcript says Alice approved the exception').first()).toBeVisible();
  await expect(page.getByText('inspection-report.pdf').first()).toBeVisible();

  await expect(page.getByText('image_caption').first()).toBeVisible();
  await expect(page.getByText('transcript').first()).toBeVisible();
  await expect(page.getByText('document_text').first()).toBeVisible();
});

test('system runtime settings persist direct target overrides and can reset to defaults', async ({ page }) => {
  await page.goto('/settings/system');
  await expect(page.getByRole('heading', { name: /^System$/i })).toBeVisible();

  await page.getByLabel('Provider timeout').fill('75');
  await page.getByLabel('Cache TTL').fill('333');
  await page.getByLabel('Rerank model').fill('jina:jina-reranker-v2-base-multilingual');
  await page.getByLabel('Rerank blend weight').fill('0.5');

  await page.getByRole('button', { name: 'Save System Settings' }).click();

  await expect(page.getByText('System settings applied to the target.')).toBeVisible();

  await page.reload();
  await expect(page.getByLabel('Provider timeout')).toHaveValue('75');
  await expect(page.getByLabel('Cache TTL')).toHaveValue('333');
  await expect(page.getByLabel('Rerank model')).toHaveValue('jina:jina-reranker-v2-base-multilingual');
  await expect(page.getByLabel('Rerank blend weight')).toHaveValue('0.5');

  await page.getByRole('button', { name: 'Reset All Overrides' }).click();
  await expect(page.getByText('All system runtime overrides were cleared back to deployment defaults.')).toBeVisible();
  await expect(page.getByLabel('Provider timeout')).toHaveValue('60');
  await expect(page.getByLabel('Cache TTL')).toHaveValue('120');
  await expect(page.getByLabel('Rerank model')).toHaveValue('cohere:rerank-v3.5');
  await expect(page.getByLabel('Rerank blend weight')).toHaveValue('0.35');
});

test('database connection overrides can be applied and cleared from system settings', async ({ page }) => {
  await page.goto('/settings/system');
  await expect(page.getByRole('heading', { name: /Database connection/i })).toBeVisible();
  await expect(page.getByText('postgres://fixture:****@127.0.0.1:5432/memstation?sslmode=disable')).toBeVisible();

  await page
    .getByLabel('Connection URI')
    .fill('postgres://operator:super-secret@10.10.0.8:5432/memstation_runtime?sslmode=disable');
  await page.getByRole('button', { name: 'Apply Database' }).click();

  await expect(page.getByText('Database connection updated and applied to the target.')).toBeVisible();
  await expect(page.getByText('postgres://operator:****@10.10.0.8:5432/memstation_runtime?sslmode=disable')).toBeVisible();
  await expect(page.getByLabel('Connection URI')).toHaveValue('');

  await page.getByRole('button', { name: 'Clear Override' }).click();
  await expect(page.getByText('Database override cleared. The target is now using the deployment default, if one exists.')).toBeVisible();
  await expect(page.getByText('postgres://fixture:****@127.0.0.1:5432/memstation?sslmode=disable')).toBeVisible();
});

test('model and provider settings persist through direct writes to the selected target', async ({ page }) => {
  await page.goto('/settings/models');
  await expect(page.getByRole('heading', { name: /^Models$/i })).toBeVisible();

  await page.getByLabel('Chat model').fill('gpt-4.1-mini-playwright');
  await page.getByLabel('Embedding model').fill('text-embedding-3-large-playwright');
  await toggleSwitch(page, 'Tool writes');
  await page.getByLabel('Max injection tokens').fill('4096');
  await page.getByRole('button', { name: 'Save settings' }).click();

  await expect(page.getByText('Configuration saved to the selected target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Chat model')).toHaveValue('gpt-4.1-mini-playwright');
  await expect(page.getByLabel('Embedding model')).toHaveValue('text-embedding-3-large-playwright');
  await expect(page.locator('.switch-row').filter({ hasText: 'Tool writes' }).locator('[role="switch"]').first()).toHaveAttribute('data-state', 'checked');
  await expect(page.getByLabel('Max injection tokens')).toHaveValue('4096');

  await page.goto('/settings/providers');
  await expect(page.getByRole('heading', { name: /^Providers$/i })).toBeVisible();

  const providerDialog = page.locator('.provider-dialog-card');
  const gatewayProvider = await ensureProviderRowVisible(page, /Gateway Providers/i, 'anthropic');
  const gatewayProviderSection = gatewayProvider.section;
  if (!gatewayProvider.openedDialog) {
    await gatewayProvider.row.getByRole('button', { name: /Edit anthropic/i }).click();
  }
  await expect(providerDialog).toBeVisible();
  await providerDialog.getByLabel('Base URL').fill('https://api.anthropic.com/v2/fixture');
  await providerDialog.getByLabel('Rotate API Key').fill('sk-playwright-anthropic');
  await providerDialog.getByRole('button', { name: 'Apply Changes' }).click();
  await gatewayProviderSection.locator('.provider-route-control .select-trigger').click();
  await page.locator('[role="option"]:visible').filter({ hasText: /^anthropic$/ }).click();
  await expect(gatewayProviderSection.locator('.provider-route-control .select-trigger')).toContainText('anthropic');
  await page.getByRole('button', { name: /Save Provider Settings/i }).click();

  await expect(page.getByText('Provider routes and credentials were applied to the selected target.')).toBeVisible();
  await page.reload();
  await expect(gatewayProviderSection.locator('.provider-route-control .select-trigger')).toContainText('anthropic');
  await gatewayProviderSection.locator('.provider-list-item').filter({ hasText: /anthropic/i }).first().getByRole('button', { name: /Edit anthropic/i }).click();
  await expect(providerDialog.getByLabel('Base URL')).toHaveValue('https://api.anthropic.com/v2/fixture');
  await providerDialog.getByRole('button', { name: 'Close' }).click();

  const rerankProvider = await ensureProviderRowVisible(page, /Rerank Providers/i, 'jina');
  const rerankProviderSection = rerankProvider.section;
  if (!rerankProvider.openedDialog) {
    await rerankProvider.row.getByRole('button', { name: /Edit jina/i }).click();
  }
  await expect(providerDialog).toBeVisible();
  await providerDialog.getByLabel('Base URL').fill('https://api.jina.ai/v2/fixture');
  await providerDialog.getByLabel('Rotate API Key').fill('jina-playwright-secret');
  await providerDialog.getByRole('button', { name: 'Apply Changes' }).click();
  await page.getByRole('button', { name: /Save Provider Settings/i }).click();

  await expect(page.getByText('Provider routes and credentials were applied to the selected target.')).toBeVisible();
  await page.reload();
  await expect(rerankProviderSection.locator('.provider-list-item').filter({ hasText: /jina/i }).first()).toBeVisible();
  await rerankProviderSection.locator('.provider-list-item').filter({ hasText: /jina/i }).first().getByRole('button', { name: /Edit jina/i }).click();
  await expect(providerDialog.getByLabel('Base URL')).toHaveValue('https://api.jina.ai/v2/fixture');
  await providerDialog.getByRole('button', { name: 'Close' }).click();
});

test('memory workspaces connect ranked recall, memory detail, and source conversation requeue flows', async ({ page }) => {
  await page.goto('/memory');
  await expect(page.getByRole('heading', { name: /^Memory Workspace$/i })).toBeVisible();

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
  await expect(page.getByRole('heading', { name: /^Memory Playground$/i })).toBeVisible();
  await page.getByLabel('Conversation ID').fill('conv-seed-1');
  await page.getByRole('button', { name: 'Inspect' }).click();
  await expect(page.getByText('summary completed')).toBeVisible();
  await expect(page.getByText('analysis completed')).toBeVisible();
  await expect(page.getByText('Gateway smoke coverage is required before rollout.')).toBeVisible();
  await page.getByLabel('Memory search query').fill('gateway rollout');
  await page.getByRole('button', { name: 'Search' }).click();
  const recallSection = page.locator('.card').filter({ has: page.getByRole('heading', { name: /^Recall$/i }) }).first();
  await expect(page.getByText(/Query embedding tokens:/)).toBeVisible();
  await expect(recallSection.getByText('MemStation launch tracks gateway rollout, memory verification, and admin observability.')).toBeVisible();
  await expect(recallSection.getByText('Keyword source')).toBeVisible();
});

test('user settings writes are visible in audit filters', async ({ page }) => {
  await page.goto('/users/admin');
  await expect(page.getByRole('heading', { name: /^admin$/i })).toBeVisible();

  await page.getByLabel('Chat model').fill('gpt-4.1-user-playwright');
  await page.getByLabel('Summary model').fill('gpt-4.1-summary-playwright');
  await toggleSwitch(page, 'Tool writes');
  await page.getByLabel('Max injection tokens').fill('3072');
  await page.getByRole('button', { name: 'Save user settings' }).click();

  await expect(page.getByText('User settings saved directly to the selected target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Chat model')).toHaveValue('gpt-4.1-user-playwright');
  await expect(page.getByLabel('Summary model')).toHaveValue('gpt-4.1-summary-playwright');
  await expect(page.getByLabel('Max injection tokens')).toHaveValue('3072');

  await page.goto('/audit?action=user_settings_update&target_type=user&target_id=admin');
  await expect(page.getByRole('heading', { name: /^Audit$/i })).toBeVisible();
  await expect(page.getByText('user_settings_update').first()).toBeVisible();
  await expect(page.getByText('gpt-4.1-user-playwright').first()).toBeVisible();
});

test('protocol playground handles responses streaming and anthropic message payloads', async ({ page }) => {
  await page.goto('/playground/protocols');
  await expect(page.getByRole('heading', { name: /^Protocols$/i })).toBeVisible();

  await page.getByLabel('API key').fill(gatewayAPIKey);

  await chooseBitsSelectOption(page, 'Protocol', 'OpenAI Responses');
  await page.getByRole('button', { name: 'Send request' }).click();
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
  await page.getByRole('button', { name: 'Send request' }).click();
  await expect(page.getByText('HTTP 200').first()).toBeVisible();
  await expect(page.getByText('"type": "message"').first()).toBeVisible();
  await expect(page.getByText('Fixture stream response for: Explain anthropic response rendering.')).toBeVisible();
});
