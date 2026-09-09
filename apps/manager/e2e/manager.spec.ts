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
  await expect(page.getByRole('heading', { name: /^Connect target$/i })).toBeVisible();
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

function sectionByHeading(page: Page, heading: RegExp) {
  return page.locator('section.provider-scope-card, section.card').filter({ has: page.getByRole('heading', { name: heading }) }).first();
}

async function fillField(page: Page, label: string, value: string): Promise<void> {
  const field = page.getByLabel(label).first();
  await field.clear();
  await field.fill(value);
}

async function ensureProviderRowVisible(page: Page, sectionLabel: RegExp, providerName: string) {
  const section = sectionByHeading(page, sectionLabel);
  const row = section.locator('.provider-list-item').filter({ hasText: new RegExp(providerName, 'i') }).first();
  if (await row.count()) {
    return { section, row, openedDialog: false };
  }

  await section.getByRole('button', { name: /^Add$/ }).click();
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
  await expect(page.getByRole('heading', { name: /^Connect target$/i })).toBeVisible();
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
  await fillField(page, 'Connection URI or base URL', fixtureBaseUrl);
  await fillField(page, 'Label', 'No Token Target');
  await page.getByRole('button', { name: 'Connect target' }).click();
  await expect(page.getByText('Admin token is required. A target cannot be connected without it.')).toBeVisible();

  const savedTargets = await page.evaluate(() => window.localStorage.getItem('memstation.manager.targets'));
  expect(savedTargets ?? '').not.toContain('No Token Target');
});

test('target manager removes saved targets from the row context menu after confirmation', async ({ page }) => {
  const removableTargetLabel = 'Removable Target';
  const removableTargetUrl = 'http://127.0.0.1:18081';

  await page.getByRole('button', { name: 'Manage targets' }).click();
  const targetDialog = page.getByRole('dialog', { name: 'Target connections' });
  await expect(targetDialog).toBeVisible();
  await expect(targetDialog.getByRole('button', { name: /^Remove$/i })).toHaveCount(0);

  await targetDialog.getByRole('button', { name: 'New target' }).click();
  const draftRow = targetDialog.locator('.target-row.is-draft').first();
  await expect(draftRow).toBeVisible();
  await expect(draftRow).toContainText('New target');
  await targetDialog.getByLabel('Connection URI or base URL').fill(removableTargetUrl);
  await targetDialog.getByLabel('Label').fill(removableTargetLabel);
  await targetDialog.getByLabel('Admin token').fill(adminToken);
  await expect(draftRow).toContainText(removableTargetLabel);
  await expect(draftRow).toContainText(removableTargetUrl);
  await targetDialog.getByRole('button', { name: 'Save target' }).click();
  await expect(targetDialog).toBeHidden();
  await page.getByRole('button', { name: 'Manage targets' }).click();
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
  await expect(page.getByText('Key Memories').first()).toBeVisible();
  await expect(page.getByText('Failed Tasks').first()).toBeVisible();
  await expect(page.getByRole('link', { name: 'Cache' })).toBeVisible();
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
  await expect(page.getByText('Filtered tokens').first()).toBeVisible();

  await expectRouteHealthy(page, '/settings/providers', /^Providers$/i);
  await expect(page.getByText('openai_compatible').first()).toBeVisible();

  await expectRouteHealthy(page, '/playground/protocols', /^Protocols$/i);
  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByRole('button', { name: 'Send request' }).click();
  await page.getByRole('tab', { name: 'Payload', exact: true }).click();
  await expect(page.getByRole('tabpanel', { name: 'Payload' }).getByText('streamed').first()).toBeVisible();
});

test('summary, impression, and fact corrections persist with audit history', async ({ page }) => {
  const updatedContent = 'Updated summary from Playwright audit validation.';
  const updatedImpression = 'Updated impression from Playwright operator review.';
  const updatedFact = 'Updated fact from Playwright operator review.';
  const correctionAudit = page.locator('.debug-block').filter({
    has: page.getByText('memory_document_update', { exact: true })
  }).last();

  await page.goto('/memory/summaries');
  await expect(page.getByRole('heading', { name: /^Summaries$/i })).toBeVisible();

  const summaryCard = page.locator('.summary-card').filter({ hasText: 'summary-1' }).first();
  await summaryCard.getByRole('link', { name: 'Open' }).click();

  await expect(page.getByRole('heading', { name: /^summary-1$/i })).toBeVisible();
  const contentField = page.locator('textarea').first();
  await contentField.fill(updatedContent);
  await page.getByRole('button', { name: 'Save changes' }).click();

  await expect(page.getByText('Summary updated and reloaded from the target.')).toBeVisible();
  await expect(page.getByText('memory_document_update')).toBeVisible();
  await correctionAudit.locator('summary').filter({ hasText: /^Event payload$/ }).click();
  await expect(page.getByText(updatedContent)).toBeVisible();

  await page.reload();
  await expect(contentField).toHaveValue(updatedContent);

  await page.goto('/memory/impressions/impression-1');
  await expect(page.getByRole('heading', { name: /^impression-1$/i })).toBeVisible();
  await page.locator('textarea').first().fill(updatedImpression);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Impression updated and reloaded from the target.')).toBeVisible();
  await correctionAudit.locator('summary').filter({ hasText: /^Event payload$/ }).click();
  await expect(page.getByText(updatedImpression)).toBeVisible();
  await page.reload();
  await expect(contentField).toHaveValue(updatedImpression);

  await page.goto('/memory/facts/fact-1');
  await expect(page.getByRole('heading', { name: /^fact-1$/i })).toBeVisible();
  await page.locator('textarea').first().fill(updatedFact);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Fact updated and reloaded from the target.')).toBeVisible();
  await correctionAudit.locator('summary').filter({ hasText: /^Event payload$/ }).click();
  await expect(page.getByText(updatedFact)).toBeVisible();
  await page.reload();
  await expect(contentField).toHaveValue(updatedFact);
});

test('key memory CRUD works end to end from the manager', async ({ page }) => {
  const keyName = 'playwright_memory_slot';
  const initialValue = 'Initial operator memory from Playwright.';
  const updatedValue = 'Updated operator memory from Playwright.';

  await page.goto('/memory/keys');
  await expect(page.getByRole('heading', { name: /^Key Memories$/i })).toBeVisible();

  await page.getByRole('button', { name: 'New key memory', exact: true }).click();
  await page.getByPlaceholder('preferred_name or recent_conversation').fill(keyName);
  await page.getByPlaceholder('profile, workflow, system').fill('workflow');
  await page.locator('textarea').first().fill(initialValue);
  await page.getByRole('button', { name: 'Create key memory' }).click();

  await expect(page.getByText('Key memory created and audited on the target.')).toBeVisible();
  const keyCard = page.locator('.keymem-card').filter({ hasText: keyName }).first();
  await expect(keyCard).toBeVisible();

  await keyCard.getByRole('button', { name: 'Edit' }).click();
  await keyCard.locator('textarea').fill(updatedValue);
  await keyCard.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Key memory updated and audited on the target.')).toBeVisible();
  await expect(keyCard.getByText(updatedValue, { exact: true })).toBeVisible();

  await keyCard.getByRole('button', { name: 'Delete' }).click();
  const deleteDialog = page.getByRole('alertdialog', { name: 'Delete key memory?' });
  await expect(deleteDialog.getByText('This action is recorded in Audit.')).toBeVisible();
  await deleteDialog.getByRole('button', { name: 'Delete key memory' }).click();
  await expect(page.locator('.keymem-card').filter({ hasText: keyName })).toHaveCount(0);
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
  const revokeDialog = page.getByRole('alertdialog', { name: 'Revoke API key?' });
  await expect(revokeDialog.getByText('This action is recorded in Audit.')).toBeVisible();
  await revokeDialog.getByRole('button', { name: 'Revoke key' }).click();
  await expect(apiKeyCard.getByText('Revoked')).toBeVisible();

  await page.goto('/operations/tasks/memory/task-2');
  await expect(page.getByRole('heading', { name: /^task-2$/i })).toBeVisible();
  await page.getByRole('button', { name: 'Run task now' }).click();
  await page.getByRole('alertdialog', { name: 'Run this task again?' }).getByRole('button', { name: 'Run task now' }).click();
  await expect(page.getByText('1 reruns')).toBeVisible();
  await expect(page.getByText('requeue_task').first()).toBeVisible();
});

test('playground preview flow streams and shows preview diff output', async ({ page }) => {
  const prompt = 'Preview memory validation';

  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /^Chat$/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(prompt);
  await page.getByRole('button', { name: /^Send$/ }).click();

  await expect(page.getByText(`Fixture stream response for: ${prompt}`)).toBeVisible();
  await page.getByRole('button', { name: 'Thinking trace' }).click();
  await expect(page.getByText('Reviewing retrieved fixture memory before composing the answer.').first()).toBeVisible();
  await expect(page.getByText('Matched keywords').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Recall' }).click();
  await expect(page.getByRole('tabpanel')).toHaveCount(1);
  await expect(page.getByRole('tabpanel', { name: 'Recall' }).getByText(`Preview summary for: ${prompt}`).first()).toBeVisible();
  await expect(page.getByRole('tabpanel', { name: 'Recall' }).getByText('Keyword source').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Diff' }).click();
  await expect(page.getByRole('tabpanel', { name: 'Diff' }).getByText('Preview', { exact: true }).first()).toBeVisible();
  await expect(page.getByRole('tabpanel', { name: 'Diff' }).getByText('validation_mode').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Raw' }).click();
  await expect(page.getByText('18 in / 14 out / 32 total')).toBeVisible();
});

test('playground persisted flow writes memory and is inspectable in admin views', async ({ page }) => {
  const prompt = 'Persist memory validation';

  await page.goto('/playground/chat');
  await expect(page.getByRole('heading', { name: /^Chat$/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  const sessionId = await page.getByPlaceholder('playground-123').inputValue();

  await toggleSwitch(page, 'Memory persist');
  await expect(page.locator('.switch-row').filter({ hasText: 'Memory persist' }).locator('[role="switch"]').first()).toHaveAttribute('data-state', 'checked');

  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(prompt);
  await page.getByRole('button', { name: 'Send & end' }).click();

  await expect(page.getByText(`Fixture stream response for: ${prompt}`)).toBeVisible();
  await page.getByRole('tab', { name: 'Inject', exact: true }).click();
  await expect(page.getByRole('tabpanel', { name: 'Inject', exact: true }).getByText('Keyword source').first()).toBeVisible();
  await page.getByRole('tab', { name: 'Recall' }).click();
  await expect(page.getByRole('tabpanel', { name: 'Recall' }).getByText(`Persisted summary for: ${prompt}`).first()).toBeVisible();
  await expect(page.getByRole('tabpanel', { name: 'Recall' }).getByText('last_validation_target').first()).toBeVisible();

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
  await expect(page.getByRole('heading', { name: /^Chat$/i })).toBeVisible();

  await page.getByPlaceholder('ms-...').fill(gatewayAPIKey);
  await page.getByPlaceholder('Configured model name').fill('gpt-4.1-mini');
  await page.getByPlaceholder('Message the target, test memory recall, or inspect persistence.').fill(slowPrompt);
  await page.getByRole('button', { name: /^Send$/ }).click();

  await expect(page.getByRole('button', { name: 'Generating…' })).toBeVisible();
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
  await expect(page.getByRole('heading', { name: /^Chat$/i })).toBeVisible();
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
  await expect(page.getByRole('heading', { name: /^Recall$/i })).toBeVisible();
  await page.locator('.field').filter({ hasText: 'Conversation ID' }).first().locator('input').fill('conv-seed-1');
  await page.getByLabel('Query').fill('launch checklist');

  await page.goto('/dashboard');
  await page.goto('/playground/memory');
  await expect(page.locator('.field').filter({ hasText: 'Conversation ID' }).first().locator('input')).toHaveValue('conv-seed-1');
  await expect(page.getByLabel('Query')).toHaveValue('launch checklist');

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
  const gatewayProvider = await ensureProviderRowVisible(page, /Gateway Providers/i, 'anthropic');
  if (!gatewayProvider.openedDialog) {
    await gatewayProvider.row.getByRole('button', { name: /Edit anthropic/i }).click();
  }
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
  await page.getByLabel('Filter').fill('validation');
  await page.getByRole('button', { name: 'New key memory', exact: true }).click();
  await page.getByPlaceholder('preferred_name or recent_conversation').fill('draft_slot');
  await page.getByPlaceholder('profile, workflow, system').fill('workflow');
  await page.locator('textarea').first().fill('Draft key memory value');

  await page.goto('/dashboard');
  await page.goto('/memory/keys');
  await expect(page.getByLabel('Filter')).toHaveValue('validation');
  await page.getByRole('button', { name: 'New key memory', exact: true }).click();
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
  await page.getByRole('alertdialog', { name: 'Reset all runtime overrides?' }).getByRole('button', { name: 'Reset all overrides' }).click();
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
  await page.getByRole('alertdialog', { name: 'Clear database override?' }).getByRole('button', { name: 'Clear override' }).click();
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

  await page.getByLabel('Query').fill('gateway memory');
  await page.getByRole('button', { name: 'Run search' }).click();
  await expect(page.getByText(/\d+ matches/)).toBeVisible();
  const summaryResult = page.locator('.memory-result-card').filter({ hasText: 'MemStation launch tracks gateway rollout' }).first();
  await expect(summaryResult).toBeVisible();
  await expect(summaryResult.getByText('Matched keywords')).toBeVisible();
  await summaryResult.getByRole('link', { name: 'Open' }).click();

  await expect(page.getByRole('heading', { name: /^summary-1$/i })).toBeVisible();
  await expect(page.getByText('memory_document_create').first()).toBeVisible();
  await page.getByRole('link', { name: 'Open source conversation' }).click();

  await expect(page.getByRole('heading', { name: /^conv-seed-1$/i })).toBeVisible();
  await page.getByRole('button', { name: 'Requeue analysis' }).click();
  await page.getByRole('alertdialog', { name: 'Requeue conversation analysis?' }).getByRole('button', { name: 'Requeue analysis' }).click();
  await expect(page.getByText('Analysis task requeued on the selected target.')).toBeVisible();
  await expect(page.locator('.message-body').filter({ hasText: 'MemStation launch tracks gateway rollout, memory verification, and admin observability.' }).first()).toBeVisible();

  await page.goto('/playground/memory');
  await expect(page.getByRole('heading', { name: /^Recall$/i })).toBeVisible();
  await page.getByLabel('Conversation ID').fill('conv-seed-1');
  await page.getByRole('button', { name: 'Inspect' }).click();
  await expect(page.getByText('summary completed')).toBeVisible();
  await expect(page.getByText('analysis completed')).toBeVisible();
  await expect(page.getByText('Gateway smoke coverage is required before rollout.')).toBeVisible();
  await page.getByLabel('Query').fill('gateway rollout');
  await page.getByRole('button', { name: 'Search' }).click();
  const recallSection = page.locator('.card').filter({ has: page.getByRole('heading', { name: 'Search results', exact: true }) }).first();
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

test('shared fields retain accessible labels after hydration and support keyboard selection', async ({ page }) => {
  await page.goto('/settings/system');
  await expect(page.getByRole('heading', { name: 'System', exact: true })).toBeVisible();
  await page.locator('label').filter({ hasText: /^Conversation timeout \(s\)$/ }).click();
  await expect(page.getByLabel('Conversation timeout (s)', { exact: true })).toBeFocused();

  const providerSelect = page.getByLabel('Gateway default provider', { exact: true });
  await providerSelect.focus();
  await page.keyboard.press('Space');
  await expect(page.getByRole('listbox')).toBeVisible();
  await page.keyboard.press('End');
  await expect(page.getByRole('option', { name: 'gemini', exact: true })).toHaveAttribute('data-highlighted', '');
  await page.keyboard.press('Enter');
  await expect(providerSelect).toBeFocused();
  await expect(providerSelect).toHaveText('gemini');

  const cookieSwitch = page.getByRole('switch', { name: 'Cross-site admin cookie', exact: true });
  const initial = await cookieSwitch.getAttribute('aria-checked');
  await page.locator('label').filter({ hasText: /^Cross-site admin cookie$/ }).click();
  await expect(cookieSwitch).toHaveAttribute('aria-checked', initial === 'true' ? 'false' : 'true');

  const missingAssociations = await page.locator('.field [aria-labelledby], .field [aria-describedby], .field label[for]').evaluateAll((elements) =>
    elements.flatMap((element) => ['for', 'aria-labelledby', 'aria-describedby'].flatMap((attribute) =>
      (element.getAttribute(attribute) ?? '').split(/\s+/).filter((id) => id && !document.getElementById(id))
    ))
  );
  expect(missingAssociations).toEqual([]);
});

test('mobile navigation traps focus, closes with Escape, and exposes target management', async ({ page, hasTouch }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const trigger = page.getByRole('button', { name: 'Open navigation', exact: true });
  await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toHaveCount(0);
  await (hasTouch ? trigger.tap() : trigger.click());
  const drawer = page.getByRole('dialog', { name: 'Navigation', exact: true });
  await expect(drawer).toBeVisible();
  for (let index = 0; index < 22; index += 1) {
    await page.keyboard.press('Tab');
    expect(await drawer.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await page.getByRole('button', { name: 'Manage targets', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Target connections' })).toBeVisible();
  await page.getByRole('button', { name: 'Actions for Fixture Target', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Delete target', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toBeVisible();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.keyboard.press('Escape');
  await (hasTouch ? trigger.tap() : trigger.click());
  await drawer.getByRole('link', { name: 'Users', exact: true }).click();
  await expect(page).toHaveURL(/\/users$/);
  await expect(drawer).toHaveCount(0);
});

test('audit filters keep typing focus and ignore responses from older filter values', async ({ page }) => {
  await page.goto('/audit');
  await expect(page.getByRole('button', { name: 'memory_document_create', exact: true })).toBeVisible();
  let releaseOld!: () => void;
  const oldResponse = new Promise<void>((resolve) => { releaseOld = resolve; });
  await page.route('**/api/admin/audit?**', async (route) => {
    const action = new URL(route.request().url()).searchParams.get('action');
    if (action === 'old') await oldResponse;
    await route.fulfill({ json: { items: [{ id: action, action, target_type: 'memory', target_id: 'fixture', created_at: new Date().toISOString() }] } });
  });
  const input = page.getByLabel('Action', { exact: true });
  const firstRequest = page.waitForRequest((request) => request.url().includes('/api/admin/audit?') && new URL(request.url()).searchParams.get('action') === 'old');
  await input.fill('old');
  await firstRequest;
  await expect(input).toBeFocused();
  await input.fill('new');
  await expect(page.getByRole('button', { name: 'new', exact: true })).toBeVisible();
  const oldFinished = page.waitForResponse((response) => response.url().includes('/api/admin/audit?') && new URL(response.url()).searchParams.get('action') === 'old');
  releaseOld();
  await (await oldFinished).finished();
  await expect(input).toHaveValue('new');
  await expect(input).toBeFocused();
  await expect(page.getByRole('button', { name: 'new', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'old', exact: true })).toHaveCount(0);
});

test('pending task confirmation cannot be dismissed or submitted twice', async ({ page }) => {
  await page.goto('/operations/tasks');
  const task = page.getByRole('button', { name: 'analysis.extract', exact: true });
  await task.focus();
  await page.keyboard.press('Enter');
  await expect(task).toHaveAttribute('aria-pressed', 'true');
  let release!: () => void;
  let writes = 0;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/api/admin/tasks/requeue', async (route) => {
    writes += 1;
    await gate;
    await route.continue();
  });
  await page.getByRole('button', { name: 'Run task now', exact: true }).click();
  const dialog = page.getByRole('alertdialog');
  await dialog.getByRole('button', { name: 'Run task now', exact: true }).click();
  await expect(dialog.getByRole('button', { name: 'Working…', exact: true })).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled();
  expect(writes).toBe(1);
  release();
  await expect(dialog).toHaveCount(0);
});

test('primary admin routes fit desktop and narrow screens without page overflow', async ({ page }) => {
  const routes = ['/audit', '/settings/system', '/settings/models', '/settings/providers', '/operations/tasks', '/memory/keys', '/users', '/conversations'];
  for (const width of [1440, 1024, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const route of routes) {
      await page.goto(route);
      await expect(page.locator('.page-title')).toBeVisible();
      await expect(page.locator('.error-state')).toHaveCount(0);
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), { message: `${route} at ${width}px` }).toBe(true);
    }
  }
});

test('dashboard range redraws its chart and section breadcrumbs resolve', async ({ page }) => {
  const canvas = page.locator('.chart-surface canvas').first();
  await expect(canvas).toBeVisible();
  let previous = '';
  await expect.poll(async () => {
    const next = await canvas.evaluate((element) => (element as HTMLCanvasElement).toDataURL());
    const stable = next === previous;
    previous = next;
    return stable;
  }, { intervals: [300, 300, 300] }).toBe(true);
  await page.getByRole('button', { name: '30d', exact: true }).click();
  await expect.poll(() => canvas.evaluate((element) => (element as HTMLCanvasElement).toDataURL())).not.toBe(previous);
  await page.goto('/settings/providers');
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Settings', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\/system$/);
  await expect(page.getByRole('heading', { name: 'System', exact: true })).toBeVisible();
});

test('mobile filters preserve choices and expose conversation results in the first viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/conversations');
  const firstResult = page.locator('.conversation-table tbody tr').first();
  await expect(firstResult).toBeInViewport();
  await expect(page.getByLabel('Date from', { exact: true })).toBeHidden();
  await page.getByRole('button', { name: /^Filters/ }).click();
  await page.getByLabel('Date from', { exact: true }).fill('2020-01-01');
  await page.getByRole('button', { name: 'Filters (1)', exact: true }).click();
  await expect(firstResult).toBeInViewport();
  await page.getByRole('button', { name: 'Filters (1)', exact: true }).click();
  await expect(page.getByLabel('Date from', { exact: true })).toHaveValue('2020-01-01');
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Filters', exact: true })).toHaveAttribute('aria-expanded', 'true');
});

test('memory search keeps its query after a failure and supports Enter to retry', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/memory');
  await expect(page.getByRole('heading', { name: 'Recent memory', exact: true })).toBeInViewport();
  let requests = 0;
  await page.route('**/api/admin/memory/search', async (route) => {
    requests += 1;
    if (requests === 1) await route.fulfill({ status: 503, json: { error: 'Search temporarily unavailable' } });
    else await route.continue();
  });
  const query = page.getByLabel('Query', { exact: true });
  await query.fill('gateway memory');
  await query.press('Enter');
  await expect(page.getByRole('alert')).toContainText('Search temporarily unavailable');
  await expect(query).toHaveValue('gateway memory');
  await query.press('Enter');
  await expect(page.locator('.memory-result-card').first()).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(0);
  expect(requests).toBe(2);
});

test('task queue changes persist and a delayed response cannot replace the selected queue', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/operations/tasks');
  await expect(page.getByRole('button', { name: 'analysis.extract', exact: true })).toBeVisible();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let memoryRequests = 0;
  await page.route('**/api/admin/tasks?**', async (route) => {
    const queue = new URL(route.request().url()).searchParams.get('queue');
    if (queue === 'memory') {
      memoryRequests += 1;
      const response = await route.fetch();
      await gate;
      await route.fulfill({ response });
    } else await route.continue();
  });
  const memoryRequest = page.waitForRequest((request) => request.url().includes('/api/admin/tasks?') && new URL(request.url()).searchParams.get('queue') === 'memory');
  await chooseBitsSelectOption(page, 'Queue', 'memory');
  await memoryRequest;
  await expect(page.getByLabel('Queue', { exact: true })).toHaveText('memory');
  await chooseBitsSelectOption(page, 'Queue', 'default');
  await expect(page.getByRole('button', { name: 'conversation.finalize', exact: true })).toBeVisible();
  const oldResponse = page.waitForResponse((response) => response.url().includes('/api/admin/tasks?') && new URL(response.url()).searchParams.get('queue') === 'memory');
  release();
  await (await oldResponse).finished();
  await expect(page.getByLabel('Queue', { exact: true })).toHaveText('default');
  await expect(page.getByRole('button', { name: 'analysis.extract', exact: true })).toHaveCount(0);
  expect(memoryRequests).toBe(1);
  await page.getByRole('button', { name: /^Filters/ }).click();
  for (const state of ['retry', 'archived', 'scheduled', 'active']) {
    await page.getByRole('button', { name: state, exact: true }).click();
  }
  await expect(page.getByText('No tasks match the current filter.', { exact: true })).toBeVisible();
});

test('short mobile dialogs keep fields, select options, and submit actions reachable', async ({ page, hasTouch }) => {
  for (const viewport of [{ width: 320, height: 568 }, { width: 844, height: 390 }]) {
    await page.setViewportSize(viewport);
    await page.goto('/memory/keys');
    await expect(page.locator('.keymem-card').first()).toBeVisible();
    const activeTab = page.getByRole('navigation', { name: 'Memory workspace sections' }).getByRole('link', { name: 'Key Memories', exact: true });
    await expect(activeTab).toBeInViewport();
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.setViewportSize(viewport);
    await expect(activeTab).toBeInViewport();
    const createButton = page.getByRole('button', { name: 'New key memory', exact: true });
    await (hasTouch ? createButton.tap() : createButton.click());
    const dialog = page.getByRole('dialog', { name: 'Create key memory', exact: true });
    await expect(dialog).toBeVisible();
    const bounds = await dialog.boundingBox();
    expect(bounds!.y).toBeGreaterThanOrEqual(0);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height);
    await dialog.getByLabel('Key', { exact: true }).fill(`mobile_${viewport.width}`);
    await dialog.getByLabel('Value', { exact: true }).fill('Mobile creation regression');
    await dialog.getByLabel('Review status', { exact: true }).click();
    const approved = page.getByRole('option', { name: 'approved', exact: true });
    await (hasTouch ? approved.tap() : approved.click());
    const submit = dialog.getByRole('button', { name: 'Create key memory', exact: true });
    await (hasTouch ? submit.tap() : submit.click());
    await expect(dialog).toHaveCount(0);
    const item = page.locator('.keymem-card').filter({ hasText: `mobile_${viewport.width}` });
    await expect(item).toBeVisible();
    await item.getByRole('button', { name: 'Delete', exact: true }).click();
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete key memory', exact: true }).click();
    await expect(item).toHaveCount(0);
  }
});

test('mobile settings keep save controls visible without covering the last fields', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/settings/system');
  const timeout = page.getByLabel('Conversation timeout (s)', { exact: true });
  await expect(timeout).toBeVisible();
  expect(await timeout.evaluate((element) => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
  const actions = page.locator('.page-header.is-sticky > .page-header-actions');
  await expect(actions).toBeInViewport();
  const lastField = page.locator('.system-subsection .field').last();
  await lastField.scrollIntoViewIfNeeded();
  const fieldBounds = await lastField.boundingBox();
  const actionBounds = await actions.boundingBox();
  expect(fieldBounds!.y + fieldBounds!.height).toBeLessThanOrEqual(actionBounds!.y);
  for (const name of ['Open navigation', 'Manage targets', 'Disconnect targets']) {
    const bounds = await page.getByRole('button', { name, exact: true }).boundingBox();
    expect(bounds!.width).toBeGreaterThanOrEqual(44);
    expect(bounds!.height).toBeGreaterThanOrEqual(44);
  }
  await page.setViewportSize({ width: 844, height: 390 });
  await page.getByRole('button', { name: 'Open navigation', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Navigation', exact: true });
  await drawer.getByRole('link', { name: 'Audit', exact: true }).click();
  await expect(page).toHaveURL(/\/audit$/);
  await expect(drawer).toHaveCount(0);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(page.locator('aside.app-sidebar')).toBeVisible();
});

test('system section navigation preserves edits across sections on desktop and mobile', async ({ page }) => {
  for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await page.goto('/settings/system');
    const navigation = page.getByRole('navigation', { name: 'System sections', exact: true });
    await page.getByLabel('Conversation timeout (s)', { exact: true }).fill('777');
    await navigation.getByRole('link', { name: 'Web', exact: true }).click();
    await expect(page.getByLabel('Rate limit RPS', { exact: true })).toBeInViewport();
    await page.getByLabel('Rate limit RPS', { exact: true }).fill('27');
    await navigation.getByRole('link', { name: 'Traffic', exact: true }).click();
    await expect(page.getByLabel('Conversation timeout (s)', { exact: true })).toHaveValue('777');
    await expect(page.getByLabel('Rate limit RPS', { exact: true })).toHaveValue('27');
    await navigation.getByRole('link', { name: 'Database connection', exact: true }).click();
    await expect(page.getByLabel('Connection URI', { exact: true })).toBeInViewport();
  }
});

test('memory detail prioritizes editing and relation links preserve user scope on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/memory/summaries/summary-1?user_id=admin');
  await expect(page.getByLabel('Content', { exact: true })).toBeInViewport();
  const details = page.locator('details').filter({ has: page.locator('summary').filter({ hasText: /^Record details$/ }) }).first();
  await expect(details).not.toHaveAttribute('open');
  await details.locator('summary').click();
  await expect(details.getByText('Node', { exact: true })).toBeVisible();
  const related = page.locator('.relation-node-link').first();
  await expect(related).toHaveAttribute('href', /user_id=admin/);
  const destination = await related.getAttribute('href');
  await related.click();
  await expect(page).toHaveURL(new RegExp(destination!.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$'));
  await expect(page.locator('.error-state')).toHaveCount(0);
});

test('manual task form is available on demand and retains its confirmation flow', async ({ page }) => {
  await page.goto('/operations/tasks');
  const panel = page.locator('details').filter({ has: page.locator('summary').filter({ hasText: /^Publish manual task$/ }) });
  await expect(panel.getByLabel('Conversation ID', { exact: true })).toBeHidden();
  await panel.locator('summary').click();
  await panel.getByLabel('Conversation ID', { exact: true }).fill('conv-seed-1');
  await panel.getByRole('button', { name: 'Publish task', exact: true }).click();
  const confirmation = page.getByRole('alertdialog', { name: 'Publish manual task?', exact: true });
  await expect(confirmation).toContainText('conv-seed-1');
  await confirmation.getByRole('button', { name: 'Publish task', exact: true }).click();
  await expect(confirmation).toHaveCount(0);
  await expect(page.getByText('Manual requeue request accepted by the selected target.', { exact: true })).toBeVisible();
});
