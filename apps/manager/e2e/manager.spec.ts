import { mkdir, writeFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';

const fixtureBaseUrl = `http://127.0.0.1:${process.env.MEMSTATION_E2E_TARGET_PORT || '18080'}`;
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

async function expectSystemAnchor(page: Page, id: string): Promise<void> {
  await expect(page.locator(`#${id}`)).toBeAttached();
  await expect.poll(() => page.evaluate(id => {
    const section = document.getElementById(id)!;
    const target = Math.max(0, Math.min(scrollY + section.getBoundingClientRect().top - Number.parseFloat(getComputedStyle(section).scrollMarginTop), document.documentElement.scrollHeight - innerHeight));
    return Math.abs(scrollY - target);
  }, id)).toBeLessThanOrEqual(1);
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
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
  await page.getByRole('option', { name: new RegExp(`^${providerName}$`, 'i') }).click();
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
    `memstation://${new URL(fixtureBaseUrl).host}?key=${encodeURIComponent(adminTokenHash)}&label=URI%20Target`
  );
  await expect(page.getByLabel('Admin token')).toHaveCount(0);
  await page.getByRole('button', { name: 'Connect target' }).click();

  await expect(page.getByRole('combobox', { name: 'Switch target' })).toContainText(new URL(fixtureBaseUrl).host);
  await page.getByRole('combobox', { name: 'Switch target' }).click();
  await expect(page.locator('.target-selector-item').filter({ hasText: new URL(fixtureBaseUrl).host })).toBeVisible();
  await expect(page.locator('.target-selector-item').filter({ hasText: 'fixture' })).toBeVisible();
  await page.keyboard.press('Escape');

  const savedTargets = await page.evaluate(() => window.localStorage.getItem('memstation.manager.targets') ?? '');
  expect(savedTargets).toContain('URI Target');
  expect(savedTargets).not.toContain(adminToken);
  expect(savedTargets).not.toContain(adminTokenHash);

  const session = await page.evaluate(async () => {
    const response = await fetch('/manager/session/fixture-target');
    return (await response.json()) as { authenticated: boolean };
  });
  expect(session.authenticated).toBe(true);
  expect((await page.context().cookies()).some((cookie) => cookie.name === 'memstation_admin_session')).toBe(false);
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

test('manager connection keeps target cookies on the server and uses only same-origin requests', async ({ page, browser }) => {
  const origins = new Set<string>();
  page.on('request', (request) => { if (['fetch', 'xhr'].includes(request.resourceType())) origins.add(new URL(request.url()).origin); });
  await expectRouteHealthy(page, '/users', /^Users$/i);
  await expectRouteHealthy(page, '/memory', /^Memory workspace$/i);
  expect([...origins]).toEqual([new URL(page.url()).origin]);
  const cookies = await page.context().cookies();
  expect(cookies.find((cookie) => cookie.name === 'memstation_manager_session')).toMatchObject({ httpOnly: true, sameSite: 'Strict' });
  expect(cookies.some((cookie) => cookie.name === 'memstation_admin_session')).toBe(false);
  const stranger = await browser.newContext();
  try {
    const response = await stranger.request.get(`${new URL(page.url()).origin}/manager/api/targets/fixture-target/api/admin/users`);
    expect(response.status()).toBe(401);
  } finally { await stranger.close(); }
  const rejected = await page.request.post('/manager/session/fixture-target', { headers: { origin: 'https://unrelated.example' }, data: { token: adminToken } });
  expect(rejected.status()).toBe(403);
  expect((await page.request.get('/manager/api/targets/unregistered/api/admin/users')).status()).toBe(404);
  expect((await page.request.get('/manager/api/targets/fixture-target/__test/reset')).status()).toBe(404);
});

test('manager connection preserves failed drafts and keeps a second target independent', async ({ page }) => {
  await page.getByRole('button', { name: 'Manage targets' }).click();
  const dialog = page.getByRole('dialog', { name: 'Target connections' });
  await dialog.getByRole('button', { name: 'New target' }).click();
  await dialog.getByLabel('Connection URI or base URL').fill('http://169.254.169.254');
  await dialog.getByLabel('Label', { exact: true }).fill('Retained draft');
  await dialog.getByLabel('Admin token').fill('wrong');
  await dialog.getByRole('button', { name: 'Save target' }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('This endpoint is not enabled in this Manager. Select an available service.')).toBeVisible();
  await expect(dialog.getByLabel('Label', { exact: true })).toHaveValue('Retained draft');
  await expect(dialog.getByLabel('Admin token')).toHaveValue('');
  const second = `http://127.0.0.1:${process.env.MEMSTATION_E2E_SECOND_TARGET_PORT || Number(process.env.MEMSTATION_E2E_TARGET_PORT || '18080') + 1}`;
  await dialog.getByLabel('Connection URI or base URL').fill(second);
  await dialog.getByLabel('Admin token').fill(adminToken);
  await dialog.getByRole('button', { name: 'Save target' }).click();
  await expect(dialog).toBeHidden();
  const origin = new URL(page.url()).origin;
  expect((await page.request.get('/manager/api/targets/second-target/api/admin/users')).ok()).toBe(true);
  expect((await page.request.delete('/manager/session/fixture-target', { headers: { origin } })).ok()).toBe(true);
  expect((await page.request.get('/manager/api/targets/fixture-target/api/admin/users')).status()).toBe(401);
  expect((await page.request.get('/manager/api/targets/second-target/api/admin/users')).ok()).toBe(true);
  await page.getByRole('button', { name: 'Manage targets' }).click();
  await expect(dialog.getByLabel('Admin token')).toHaveValue('');
});

test('target manager removes saved targets from the row context menu after confirmation', async ({ page }) => {
  const removableTargetLabel = 'Removable Target';
  const removableTargetUrl = `http://127.0.0.1:${process.env.MEMSTATION_E2E_SECOND_TARGET_PORT || Number(process.env.MEMSTATION_E2E_TARGET_PORT || '18080') + 1}`;

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
  await expect(summaryCard.getByRole('link', { name: 'Open' })).toHaveAttribute('href', /section=memory/);
  await page.goto('/memory/summaries/summary-1?user_id=admin');

  await expect(page.getByRole('heading', { name: /^summary-1$/i })).toBeVisible();
  const contentField = page.locator('textarea').first();
  await contentField.fill(updatedContent);
  await page.getByRole('button', { name: 'Save changes' }).click();

  await expect(page.getByText('Summary updated and reloaded from the target.')).toBeVisible();
  await expect(page.getByText('memory_document_update')).toBeVisible();
  await correctionAudit.getByRole('button', { name: 'Event payload', exact: true }).click();
  await expect(page.getByText(updatedContent)).toBeVisible();

  await page.reload();
  await expect(contentField).toHaveValue(updatedContent);

  await page.goto('/memory/impressions/impression-1');
  await expect(page.getByRole('heading', { name: /^impression-1$/i })).toBeVisible();
  await page.locator('textarea').first().fill(updatedImpression);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Impression updated and reloaded from the target.')).toBeVisible();
  await correctionAudit.getByRole('button', { name: 'Event payload', exact: true }).click();
  await expect(page.getByText(updatedImpression)).toBeVisible();
  await page.reload();
  await expect(contentField).toHaveValue(updatedImpression);

  await page.goto('/memory/facts/fact-1');
  await expect(page.getByRole('heading', { name: /^fact-1$/i })).toBeVisible();
  await page.locator('textarea').first().fill(updatedFact);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Fact updated and reloaded from the target.')).toBeVisible();
  await correctionAudit.getByRole('button', { name: 'Event payload', exact: true }).click();
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

  await chooseBitsSelectOption(page, 'Agent', 'Coding agent');
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
  await page.getByRole('textbox', { name: 'Filter', exact: true }).fill('validation');
  await page.getByRole('button', { name: 'New key memory', exact: true }).click();
  await page.getByPlaceholder('preferred_name or recent_conversation').fill('draft_slot');
  await page.getByPlaceholder('profile, workflow, system').fill('workflow');
  await page.locator('textarea').first().fill('Draft key memory value');

  await page.goto('/dashboard');
  await page.goto('/memory/keys');
  await expect(page.getByRole('textbox', { name: 'Filter', exact: true })).toHaveValue('validation');
  await page.getByRole('button', { name: 'New key memory', exact: true }).click();
  await expect(page.getByPlaceholder('preferred_name or recent_conversation')).toHaveValue('draft_slot');
  await expect(page.getByPlaceholder('profile, workflow, system')).toHaveValue('workflow');
  await expect(page.locator('textarea').first()).toHaveValue('Draft key memory value');
});

test('conversation detail renders multimodal tool payloads from raw messages and timeline parts', async ({ page }) => {
  await page.goto('/conversations/conv-mm-tool-1');
  await expect(page.getByRole('heading', { name: /^conv-mm-tool-1$/i })).toBeVisible();

  await page.getByRole('button', { name: 'Tool calls (1)' }).click();
  await expect(page.getByText('Tool input media, tool_input.attachments[0]')).toBeVisible();
  await expect(page.getByAltText('diagram-tool.png').first()).toBeVisible();
  await expect(page.getByText('tool-brief.pdf').first()).toBeVisible();

  await page.getByRole('button', { name: 'Tool results (1)' }).click();
  await expect(page.getByText('Tool output media, tool_output.content[1]')).toBeVisible();
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
  await chooseBitsSelectOption(page, 'Rerank provider', 'Jina');
  await page.getByLabel('Rerank model').fill('jina-reranker-v2-base-multilingual');

  await page.getByRole('button', { name: 'Save System Settings' }).click();

  await expect(page.getByText('System settings applied to the target.')).toBeVisible();

  await page.reload();
  await expect(page.getByLabel('Provider timeout')).toHaveValue('75');
  await expect(page.getByLabel('Cache TTL')).toHaveValue('333');
  await expect(page.getByLabel('Rerank model')).toHaveValue('jina-reranker-v2-base-multilingual');

  await page.getByRole('button', { name: 'Reset All Overrides' }).click();
  await page.getByRole('alertdialog', { name: 'Reset all runtime overrides?' }).getByRole('button', { name: 'Reset all overrides' }).click();
  await expect(page.getByText('All system runtime overrides were cleared back to deployment defaults.')).toBeVisible();
  await expect(page.getByLabel('Provider timeout')).toHaveValue('60');
  await expect(page.getByLabel('Cache TTL')).toHaveValue('120');
  await expect(page.getByLabel('Rerank model')).toHaveValue('rerank-v3.5');
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
  await page.getByRole('option', { name: 'anthropic', exact: true }).click();
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
  await page.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(page).toHaveURL(/\/w\/personal\/memory\/summary\/summary-1/);
  await expect(page.getByRole('heading', { name: 'Memory document', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Open source conversation' }).click();
  await expect(page).toHaveURL(/\/w\/personal\/conversations\/conv-seed-1/);
  await expect(page.getByRole('link', { name: 'Workspace processing and rebuilds', exact: true })).toHaveAttribute('href','/t/fixture-target/w/personal/lifecycle');
  await page.goto('/conversations/conv-seed-1');

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
  await expect(page.getByRole('status')).toHaveText('Loading audit events…');
  await expect(page.getByText('No audit events match the current filter.', { exact: true })).toHaveCount(0);
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

interface LayoutBounds { left: number; right: number; width: number }
interface PageLayoutMeasurement { route: string; width: number; toolbar: LayoutBounds; content: LayoutBounds; roots: LayoutBounds[]; overflow: boolean; overflowingFields: string[] }

const layoutRoutes = [
  '/dashboard', '/audit', '/settings/system', '/settings/models', '/settings/providers',
  '/operations/tasks', '/operations/tasks/memory/task-2', '/operations/health',
  '/operations/queues', '/operations/queues/default', '/operations/cache',
  '/users', '/users/admin', '/users/admin/agents', '/users/admin/agents/coding-agent',
  '/conversations', '/conversations/conv-seed-1', '/statistics/tokens',
  '/playground/chat', '/playground/memory', '/playground/protocols',
  '/memory', '/memory/summaries', '/memory/impressions', '/memory/facts', '/memory/items', '/memory/keys',
  '/memory/summaries/summary-1?user_id=admin',
  '/t/fixture-target/workspaces',
  '/t/fixture-target/w/personal/memory', '/t/fixture-target/w/personal/memory/search',
  '/t/fixture-target/w/personal/memory/summary/summary-1',
  '/t/fixture-target/w/personal/memory/fact/fact-1',
  '/t/fixture-target/w/personal/keys', '/t/fixture-target/w/personal/keys/new',
  '/t/fixture-target/w/personal/keys/key-1',
  '/t/fixture-target/w/personal/conversations', '/t/fixture-target/w/personal/conversations/conv-seed-1',
  '/t/fixture-target/w/personal/settings', '/t/fixture-target/w/personal/access',
  '/t/fixture-target/w/personal/access/coding-agent',
  ...['activity', 'review', 'sharing', 'connections', 'data'].map(view => `/t/fixture-target/w/personal/lifecycle?view=${view}`)
];

for (const width of [2560, 1920, 1440, 1024, 768, 390, 320]) {
  test(`primary admin routes fit ${width}px screens without page overflow`, async ({ page }, testInfo) => {
    // Each case now checks every page family, including the detail and editor routes.
    test.setTimeout(120_000);
    const measurements: PageLayoutMeasurement[] = [];
    await page.setViewportSize({ width, height: 1000 });
    for (const route of layoutRoutes) {
      await test.step(`${route} at ${width}px`, async () => {
        await page.goto(route);
        await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
        await expect(page.locator('.loading-state')).toHaveCount(0);
        if (route === '/audit') await expect(page.getByRole('button', { name: 'memory_document_create', exact: true })).toBeVisible();
        await expect(page.locator('.error-state')).toHaveCount(0);
        const frame = await page.evaluate(() => {
          const box = (element: Element) => { const rect = element.getBoundingClientRect(); return { left: rect.left, right: rect.right, width: rect.width }; };
          const toolbar = box(document.querySelector('.app-topbar')!);
          const content = document.querySelector('.app-content')!;
          const roots = [...content.children].filter(element => element.getBoundingClientRect().width > 0).map(box);
          const overflowingFields = [...content.querySelectorAll('.field')].flatMap(field => {
            const boundary = box(field);
            if (!boundary.width) return [];
            const overflow = [...field.querySelectorAll(':scope > .input, :scope > .textarea, :scope > .select-trigger')].some(control => {
              const rect = box(control);
              return rect.width > 0 && (rect.left < boundary.left - 1 || rect.right > boundary.right + 1);
            });
            return overflow ? [field.querySelector('.field-label')?.textContent ?? 'Unlabelled field'] : [];
          });
          return { toolbar, content: box(content), roots, overflow: document.documentElement.scrollWidth > innerWidth, overflowingFields };
        });
        measurements.push({ route, width, ...frame });
        expect(frame.overflow, `${route} at ${width}px`).toBe(false);
        expect(frame.overflowingFields, `${route} controls within their fields`).toEqual([]);
        for (const region of [frame.content, ...frame.roots]) {
          expect(Math.abs(region.left - frame.toolbar.left), `${route} left edge`).toBeLessThanOrEqual(1);
          expect(Math.abs(region.right - frame.toolbar.right), `${route} right edge`).toBeLessThanOrEqual(1);
        }
        expect(frame.content.width).toBeLessThanOrEqual(1600);
        if ((width === 1440 || width === 390) && testInfo.project.name === 'chromium') {
          const name = route.slice(1).replaceAll('/', '-').replaceAll('?', '-').replaceAll('=', '-');
          await page.screenshot({ path: testInfo.outputPath(`ui-${name}-${width}.png`), fullPage: true, animations: 'disabled' });
        }
      });
    }
    await mkdir(testInfo.outputDir, { recursive: true });
    const report = testInfo.outputPath('page-layout-measurements.json');
    await writeFile(report, JSON.stringify(measurements, null, 2));
    await testInfo.attach('page-layout-measurements', { path: report, contentType: 'application/json' });
  });
}

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
  await expect(page.getByLabel('Date from', { exact: true })).toHaveValue('');
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
  const details = page.locator('.disclosure').filter({ has: page.getByRole('button', { name: 'Record details', exact: true }) }).first();
  await expect(details.getByRole('button', { name: 'Record details', exact: true })).toHaveAttribute('aria-expanded', 'false');
  await details.getByRole('button', { name: 'Record details', exact: true }).click();
  await expect(details.getByText('Node', { exact: true })).toBeVisible();
  const related = page.locator('.relation-node-link').first();
  await expect(related).toHaveAttribute('href', /user_id=admin/);
  await expect(related).toHaveAttribute('href', /\/t\/fixture-target\/workspaces\?section=memory/);
  await related.click();
  await page.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(page).toHaveURL(/\/t\/fixture-target\/w\/personal\/memory\//);
  await expect(page.locator('.error-state')).toHaveCount(0);
});

test('manual task form is available on demand and retains its confirmation flow', async ({ page }) => {
  await page.goto('/operations/tasks');
  const panel = page.locator('.disclosure').filter({ has: page.getByRole('button', { name: 'Publish manual task', exact: true }) });
  await expect(panel.getByLabel('Conversation ID', { exact: true })).toBeHidden();
  await panel.getByRole('button', { name: 'Publish manual task', exact: true }).click();
  await panel.getByLabel('Conversation ID', { exact: true }).fill('conv-seed-1');
  await panel.getByRole('button', { name: 'Publish task', exact: true }).click();
  const confirmation = page.getByRole('alertdialog', { name: 'Publish manual task?', exact: true });
  await expect(confirmation).toContainText('conv-seed-1');
  await confirmation.getByRole('button', { name: 'Publish task', exact: true }).click();
  await expect(confirmation).toHaveCount(0);
  await expect(page.getByText('Manual requeue request accepted by the selected target.', { exact: true })).toBeVisible();
});


test('memory historical filters preserve their values and send distinct time parameters', async ({ page }) => {
  await page.goto('/memory');
  await page.getByRole('button', { name: /^Filters/ }).click();
  await page.getByLabel('Facts valid at').fill('2026-02-01T10:30');
  await page.getByLabel('Known by').fill('2026-03-01T11:30');
  await page.getByLabel('Entity scope').fill('work');
  await page.getByRole('button', { name: /^Filters/ }).click();
  const request = page.waitForRequest((req) => req.url().includes('/api/admin/memory/search') && req.method() === 'POST');
  await page.getByLabel('Query', { exact: true }).fill('gateway');
  await page.getByRole('button', { name: 'Run search', exact: true }).click();
  const body = (await request).postDataJSON();
  expect(body.as_of).toBeTruthy(); expect(body.known_at).toBeTruthy();
  expect(body.as_of).not.toBe(body.known_at);
  expect(body.entity_scope).toBe('work'); expect(body.relation_mode).toBe('neighborhood');
  await page.getByRole('button', { name: /^Filters/ }).click();
  await expect(page.getByLabel('Facts valid at')).toHaveValue('2026-02-01T10:30');
});

test('entity alias candidates require confirmation and can be revoked after reload', async ({ page }) => {
  await page.goto('/memory/entities/entity-station?user_id=admin');
  await expect(page.getByRole('heading', {name:'Entity names and merges'})).toBeVisible();
  await page.getByLabel('Alias', {exact:true}).fill('记忆站');
  await page.getByLabel('Mapping reason').fill('Confirmed project nickname from the operator');
  await page.getByRole('button', {name:'Save candidate'}).click();
  await expect(page.locator('.metadata-row').filter({ hasText: 'work' }).getByText('candidate', { exact: true })).toBeVisible();
  await page.getByRole('button', {name:'Confirm mapping',exact:true}).click();
  await page.getByRole('alertdialog').getByRole('button', {name:'Confirm',exact:true}).click();
  await expect(page.locator('.metadata-row').filter({ hasText: 'work' }).getByText('confirmed', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.locator('.metadata-row').filter({ hasText: 'work' }).getByText('confirmed', { exact: true })).toBeVisible();
  await page.getByRole('button', {name:'Revoke mapping'}).click();
  await page.getByRole('alertdialog').getByRole('button', {name:'Revoke',exact:true}).click();
  await expect(page.locator('.metadata-row').filter({ hasText: 'work' }).getByText('revoked', { exact: true })).toBeVisible();
  await expect(page.locator('.error-state')).toHaveCount(0);
});


test('shared controls preserve drafts, keyboard focus, and compact mobile layout', async ({ page }, testInfo) => {
  await page.goto('/operations/tasks');
  const generation = page.getByRole('button', { name: 'Generation lifecycle', exact: true });
  await expect(generation).toHaveAttribute('aria-expanded', 'true');
  await generation.click();
  await expect(generation).toHaveAttribute('aria-expanded', 'false');
  const trigger = page.getByRole('button', { name: 'Publish manual task', exact: true });
  await expect(trigger).toHaveAttribute('aria-expanded', 'false');
  await trigger.focus();
  await page.keyboard.press('Enter');
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');
  const input = page.getByLabel('Conversation ID', { exact: true });
  await input.fill('conv-control-draft');
  const select = page.getByRole('combobox', { name: 'Task type', exact: true });
  await select.focus();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('listbox')).toBeVisible();
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  const selected = await select.textContent();
  await expect(page.getByRole('listbox')).toBeHidden();
  await expect(select).toBeFocused();
  await select.click();
  await page.keyboard.press('Escape');
  await expect(select).toBeFocused();
  await trigger.focus();
  await page.keyboard.press('Space');
  await expect(input).toBeHidden();
  await expect(trigger).toHaveAttribute('aria-expanded', 'false');
  await page.keyboard.press('Enter');
  await expect(input).toHaveValue('conv-control-draft');
  await expect(select).toHaveText(selected!);
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await input.scrollIntoViewIfNeeded();
    const inputBox = await input.boundingBox();
    const selectBox = await select.boundingBox();
    expect(Math.abs(inputBox!.height - selectBox!.height)).toBeLessThan(1);
    if (width < 1024) {
      expect(inputBox!.height).toBeGreaterThanOrEqual(44);
      expect(await input.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`shared-controls-${width}.png`), fullPage: true, animations: 'disabled' });
  }
  await page.getByRole('combobox', { name: 'Switch target', exact: true }).click();
  const targetOption = page.getByRole('option').first();
  await expect(targetOption).toBeVisible();
  expect((await targetOption.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await page.keyboard.press('Escape');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await trigger.click();
  await expect(input).toBeHidden();
});


test('agent credential permissions and mobile scope stay explicit', async ({ page }, testInfo) => {
  await page.goto('/users/admin');
  await expect(page.getByText('Choose an active agent.', { exact: false })).toBeVisible();
  await chooseBitsSelectOption(page, 'Agent', 'Coding agent');
  await chooseBitsSelectOption(page, 'Key permissions', 'Read memory only');
  await chooseBitsSelectOption(page, 'Expires after', '7 days');
  await page.getByLabel('New Key Label').fill('Scoped reader');
  const issued = page.waitForRequest((request) => request.method() === 'POST' && request.url().endsWith('/users/admin/api-keys'));
  const issuedResponse = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/users/admin/api-keys'));
  await page.getByRole('button', { name: 'Create API Key', exact: true }).click();
  const body = (await issued).postDataJSON();
  expect(body.agent_id).toBe('coding-agent');
  expect(body.capabilities).toEqual(['memory.read']);
  expect(Date.parse(body.expires_at) - Date.now()).toBeGreaterThan(6 * 86400000);
  await expect(page.getByText('Coding agent Personal workspace')).toBeVisible();
  await expect(page.getByText('Permissions: memory.read', { exact: true })).toBeVisible();
  const raw = (await (await issuedResponse).json()).api_key;
  const leaked = await page.evaluate((secret) => [localStorage, sessionStorage].some((storage) => Object.values(storage).some((value) => String(value).includes(secret))), raw);
  expect(leaked).toBe(false);
  await page.screenshot({ path: testInfo.outputPath('agent-credentials.png'), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.reload();
  await expect(page.getByText('Coding agent Personal workspace')).toBeVisible();
  await expect(page.getByText(raw, { exact: true })).toHaveCount(0);
});

test('agent management lifecycle and credential ceilings', async ({ page }, testInfo) => {
  await page.goto('/users/admin');
  await page.getByRole('link', { name: 'Manage agents' }).click();
  await page.getByRole('button', { name: 'Create agent', exact: true }).click();
  const create = page.getByRole('dialog');
  await create.getByLabel('Agent name', { exact: true }).fill('Group assistant');
  await create.getByRole('button', { name: 'Create agent', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Group assistant', exact: true })).toBeVisible();
  await page.getByLabel('Agent name', { exact: true }).fill('Project assistant');
  await page.getByRole('button', { name: 'Save name' }).click();
  await expect(page.getByRole('heading', { name: 'Project assistant', exact: true })).toBeVisible();
  await page.getByRole('checkbox', { name: /^Call models/ }).uncheck();
  await page.getByRole('button', { name: 'Save permissions' }).click();
  await expect(page.getByText('Permissions saved.', { exact: false })).toBeVisible();
  await page.getByRole('button', { name: 'Issue credential', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Credential name').fill('Laptop reader');
  await expect(dialog.getByRole('checkbox', { name: /^Call models/ })).toBeDisabled();
  await dialog.getByRole('checkbox', { name: /^Read files/ }).uncheck();
  const response = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/users/admin/api-keys'));
  await dialog.getByRole('button', { name: 'Issue credential', exact: true }).click();
  const issued = await response;
  expect(issued.request().postDataJSON().capabilities).toEqual(['memory.read']);
  const raw = (await issued.json()).api_key;
  await expect(page.getByRole('region', { name: 'New API key' })).toBeVisible();
  expect(await page.evaluate((secret) => [localStorage, sessionStorage].some((s) => Object.values(s).some((v) => String(v).includes(secret))), raw)).toBe(false);
  await page.getByRole('button', { name: 'Hide key' }).click();
  await expect(page.getByText(raw, { exact: true })).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('agent-access.png'), fullPage: true });
  await page.getByRole('button', { name: 'Disable agent', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Disable agent', exact: true }).click();
  await expect(page.getByText('Agent disabled and all its credentials revoked.')).toBeVisible();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Issue credential', exact: true })).toBeDisabled();
  await expect(page.locator('.credential-row')).toContainText('Revoked');
  await expect(page.locator('.credential-row')).toContainText('Effective: No operational access');
  await page.getByRole('main').getByRole('button', { name: 'Enable agent', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: 'Enable agent', exact: true }).click();
  await expect(page.getByText('Agent enabled. Issue a new credential to reconnect it.')).toBeVisible();
  await expect(page.locator('.credential-row')).toContainText('Revoked');
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Project assistant', exact: true })).toBeVisible();
  await expect(page.getByText(raw, { exact: true })).toHaveCount(0);
  await page.setViewportSize({ width: 320, height: 568 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Issue credential', exact: true }).click();
  await page.setViewportSize({ width: 320, height: 390 });
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toHaveCount(0);
});

test('agent management preserves edits on revision conflict', async ({ page }) => {
  await page.goto('/users/admin/agents/coding-agent');
  await expect(page.getByRole('heading', { name: 'Coding agent', exact: true })).toBeVisible();
  await page.getByRole('checkbox', { name: /^Edit memory/ }).check();
  const changed = await page.request.put('/manager/api/targets/fixture-target/api/admin/users/admin/agents/coding-agent/grant', { headers: { origin: new URL(page.url()).origin }, data: { capabilities: ['memory.read'], expected_revision: 1 } });
  expect(changed.ok()).toBe(true);
  await page.getByRole('button', { name: 'Save permissions' }).click();
  await expect(page.getByText('Your changes are still in the form.', { exact: false })).toBeVisible();
  await expect(page.getByRole('checkbox', { name: /^Edit memory/ })).toBeChecked();
  await expect(page.getByRole('button', { name: 'Save permissions' })).toBeDisabled();
  await page.getByRole('button', { name: 'Discard edits and reload' }).click();
  await expect(page.getByRole('checkbox', { name: /^Edit memory/ })).not.toBeChecked();
  await expect(page.getByText('Currently effective: Read memory.')).toBeVisible();
});

test('agent management search pagination and stale scope responses', async ({ page }) => {
  const records = Array.from({ length: 105 }, (_, i) => ({ id: `agent-${String(i).padStart(3, '0')}`, name: `Worker ${i}`, status: 'active' }));
  await page.route('**/manager/api/targets/fixture-target/api/admin/users/admin/agents?*', async (route) => {
    const url = new URL(route.request().url());
    const query = url.searchParams.get('q') ?? '';
    const matches = records.filter((item) => item.id > (url.searchParams.get('after') ?? '') && item.name.includes(query));
    const items = matches.slice(0, 50);
    await route.fulfill({ json: { items, next_cursor: matches.length > 50 ? items.at(-1)!.id : '' } });
  });
  await page.goto('/users/admin/agents');
  await expect(page.locator('.agent-row')).toHaveCount(50);
  await page.getByRole('button', { name: 'Load more agents' }).click();
  await expect(page.locator('.agent-row')).toHaveCount(100);
  await page.getByRole('button', { name: 'Load more agents' }).click();
  await expect(page.locator('.agent-row')).toHaveCount(105);
  await page.getByLabel('Search agents').fill('Worker 104');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page.locator('.agent-row')).toHaveCount(1);
  await expect(page.locator('.agent-row')).toContainText('Worker 104');
  await page.unroute('**/manager/api/targets/fixture-target/api/admin/users/admin/agents?*');
  // Hold an old identity response across client-side route changes.
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/manager/api/targets/fixture-target/api/admin/users/admin/agents/coding-agent', async (route) => {
    const response = await route.fetch();
    await held;
    await route.fulfill({ response }).catch(() => {});
  });
  await page.goto('/users/admin/agents/coding-agent');
  await page.getByRole('link', { name: 'All agents', exact: true }).click();
  await page.locator('.agent-row').filter({ hasText: 'Review agent' }).click();
  await expect(page.getByRole('heading', { name: 'Review agent', exact: true })).toBeVisible();
  release();
  await expect(page.getByRole('heading', { name: 'Coding agent', exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Agent name', { exact: true })).toHaveValue('Review agent');
});


test('manager connection keeps preferences when disconnect fails and supports retry', async ({ page }) => {
  await page.route('**/manager/session/fixture-target', async (route) => {
    if (route.request().method() === 'DELETE') await route.fulfill({ status: 503, json: { message: 'Session storage unavailable' } });
    else await route.continue();
  });
  await page.getByRole('button', { name: 'Disconnect targets', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Some connections could not be disconnected');
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('memstation.manager.targets') ?? '[]').length)).toBe(1);
  await page.unroute('**/manager/session/fixture-target');
  await page.getByRole('button', { name: 'Disconnect targets', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('memstation.manager.targets') ?? '[]').length)).toBe(0);
});


test('workspace management saves policy and keeps agent navigation scoped', async ({ page }, testInfo) => {
  await resetFixture(page); await configureTarget(page); await login(page);
  await page.goto('/t/fixture-target/workspaces');
  await expect(page.getByRole('heading', { name: 'Workspaces', exact: true })).toBeVisible();
  await page.getByLabel('Owner ID', { exact: true }).fill('admin');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page).toHaveURL(/user_id=admin/);
  await expect(page.getByRole('link', { name: /Bob notes/ })).toHaveCount(0);
  await page.getByRole('link', { name: /Personal.*Owner: admin/ }).click();
  await page.getByLabel('Workspace name').fill('Shared personal notes');
  await page.getByRole('checkbox', { name: /^Read memory/ }).uncheck();
  await page.getByRole('button', { name: 'Save workspace', exact: true }).click();
  await expect(page.getByText('Workspace saved.', { exact: false })).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('workspace-settings.png'), fullPage: true });
  await page.getByRole('link', { name: 'Agents with access', exact: true }).click();
  await page.getByRole('link', { name: /Coding agent/ }).click();
  await expect(page).toHaveURL(/\/t\/fixture-target\/w\/personal\/access\/coding-agent$/);
  await expect(page.getByLabel('Agent name', { exact: true })).toHaveValue('Coding agent');
  await page.getByRole('link', { name: 'All agents', exact: true }).click();
  await expect(page).toHaveURL(/\/t\/fixture-target\/w\/personal\/access$/);
  await page.getByRole('link', { name: 'Workspace settings', exact: true }).click();
  await expect(page.getByLabel('Workspace name')).toHaveValue('Shared personal notes');
  await expect(page.getByRole('checkbox', { name: /^Read memory/ })).not.toBeChecked();
});

test('workspace management preserves draft through conflicts and scope changes', async ({ page }) => {
  await resetFixture(page); await configureTarget(page); await login(page);
  await page.goto('/t/fixture-target/w/personal/settings');
  await page.getByLabel('Workspace name').fill('Local draft');
  const changed = await page.request.put('/manager/api/targets/fixture-target/api/admin/workspaces/personal', {
    headers: { origin: new URL(page.url()).origin }, data: { user_id: 'admin', name: 'Remote edit', capabilities: ['memory.read'], expected_policy_version: 1 }
  });
  expect(changed.ok()).toBeTruthy();
  await page.getByRole('button', { name: 'Save workspace', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Your draft is preserved');
  await expect(page.getByLabel('Workspace name')).toHaveValue('Local draft');
  await expect(page.getByRole('button', { name: 'Use draft against this version' })).toBeDisabled();
  await page.getByRole('button', { name: 'Load current version' }).click();
  await expect(page.getByRole('heading', { name: 'Remote edit', exact: true })).toBeVisible();
  await expect(page.getByLabel('Workspace name')).toHaveValue('Local draft');
  await page.getByRole('button', { name: 'Use draft against this version' }).click();
  await page.getByRole('button', { name: 'Save workspace', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Local draft', exact: true })).toBeVisible();
  await page.getByLabel('Workspace name').fill('Retained local draft');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('link', { name: 'All workspaces', exact: true }).click();
  await page.getByRole('link', { name: /Bob notes/ }).click();
  await expect(page.getByLabel('Workspace name')).toHaveValue('Bob notes');
  await page.goBack();
  await page.getByRole('link', { name: /Local draft.*Owner: admin/ }).click();
  await expect(page.getByLabel('Workspace name')).toHaveValue('Retained local draft');
  await page.getByRole('button', { name: 'Discard draft' }).click();
  await expect(page.getByLabel('Workspace name')).toHaveValue('Local draft');
});

test('workspace management deep links select the named service and reject missing scopes', async ({ page }) => {
  await resetFixture(page); await configureTarget(page); await login(page);
  const secondBase = `http://127.0.0.1:${process.env.MEMSTATION_E2E_SECOND_TARGET_PORT || String(Number(process.env.MEMSTATION_E2E_TARGET_PORT || '18080') + 1)}`;
  await page.request.post(`${secondBase}/__test/reset`);
  const connected = await page.request.post('/manager/session/second-target', { headers: { origin: new URL(page.url()).origin }, data: { token: adminToken } });
  expect(connected.ok()).toBeTruthy();
  await page.request.put('/manager/api/targets/second-target/api/admin/workspaces/personal', { headers: { origin: new URL(page.url()).origin }, data: { user_id: 'admin', name: 'Second service notes', capabilities: ['memory.read'], expected_policy_version: 1 } });
  await page.goto('/t/second-target/w/personal/settings');
  await expect(page.getByLabel('Workspace name')).toHaveValue('Second service notes');
  await expect(page.getByRole('combobox', { name: 'Switch target' })).toContainText(new URL(secondBase).host);
  await page.goto('/t/fixture-target/w/personal/settings');
  await expect(page.getByLabel('Workspace name')).toHaveValue('Personal');
  await page.goto('/t/fixture-target/w/missing/settings');
  await expect(page.getByRole('alert')).toContainText('no longer exists');
  await expect(page.getByLabel('Workspace name')).toHaveCount(0);
  await page.goto('/t/not-registered/workspaces');
  await expect(page.getByText('This service is not registered in Manager.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Workspaces', exact: true })).toHaveCount(0);
});

test('workspace management ignores late scope responses', async ({ page }) => {
  await resetFixture(page); await configureTarget(page); await login(page);
  await page.goto('/t/fixture-target/workspaces');
  let release: (() => void) | undefined;
  let started: (() => void) | undefined;
  const pending = new Promise<void>((resolve) => { started = resolve; });
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/t/fixture-target/w/personal/settings/__data.json*', async (route) => {
    const response = await route.fetch(); started?.(); await held; await route.fulfill({ response });
  });
  await page.getByRole('link', { name: /Personal.*Owner: admin/ }).click({ noWaitAfter: true });
  await pending;
  await page.getByRole('link', { name: /Bob notes/ }).click();
  await expect(page.getByLabel('Workspace name')).toHaveValue('Bob notes');
  release?.();
  await page.waitForLoadState('networkidle');
  await expect(page.getByLabel('Workspace name')).toHaveValue('Bob notes');
});

test('workspace management semantic episodes show state time wording and source links', async ({ page }, testInfo) => {
  await page.request.post(`${fixtureBaseUrl}/__test/episode-seed`);
  const base = '/t/fixture-target/w/personal';
  await page.goto(`${base}/memory`);
  await chooseBitsSelectOption(page, 'Document type', 'Semantic episodes');
  await page.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(page).toHaveURL(/kind=episode/);
  await page.getByRole('link', { name: /Alice cancelled.*episode-fixture/ }).click();
  await expect(page).toHaveURL(/\/memory\/episode\/episode-fixture/);
  const details = page.getByRole('region', { name: 'Semantic episode context' });
  await expect(details.getByText('cancelled', { exact: true })).toBeVisible();
  await expect(details.getByText('Alice', { exact: true })).toBeVisible();
  await expect(details.getByText('next Friday', { exact: true })).toBeVisible();
  await expect(details.getByText('2026-10-23T09:00:00Z', { exact: true })).toBeVisible();
  await expect(details.getByText('9:00 am on 23 October, 2026', { exact: true })).toBeVisible();
  await expect(page.getByText('Alice: I cancelled the Friday visit.', { exact: true })).toBeVisible();
  for (const width of testInfo.project.name === 'mobile-webkit' ? [320, 390] : [1280]) {
    await page.setViewportSize({ width, height: 850 });
    await expect(details).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`semantic-episode-${width}.png`), fullPage: true });
  }
  await page.getByRole('link', { name: 'Open source conversation', exact: true }).click();
  await expect(page).toHaveURL(/\/conversations\/conv-seed-1/);
});

test('workspace management browses memory and conversations without leaving scope', async ({ page }, testInfo) => {
  const base = '/t/fixture-target/w/personal';
  await page.goto(`${base}/memory`);
  await expect(page.getByRole('heading', { name: 'Memory documents', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: /Retained conversation segment.*segment-browse/ })).toBeVisible();
  await page.getByLabel('Filter documents').fill('Gateway smoke');
  await page.locator('.field').filter({ hasText: 'Document type' }).locator('.select-trigger').click();
  await page.getByRole('option', { name: 'fact', exact: true }).click();
  await page.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(page).toHaveURL(/kind=fact/);
  await page.reload();
  await expect(page.getByLabel('Filter documents')).toHaveValue('Gateway smoke');
  await page.getByRole('link', { name: /Gateway smoke coverage.*fact/ }).click();
  await expect(page).toHaveURL(/\/w\/personal\/memory\/fact\/fact-1/);
  await expect(page.getByRole('button', { name: 'Save changes' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /supports.*summary-1/ })).toHaveAttribute('href', /\/w\/personal\/memory\/summary\/summary-1/);
  await page.getByRole('link', { name: 'Open source conversation', exact: true }).click();
  await expect(page).toHaveURL(/\/w\/personal\/conversations\/conv-seed-1/);
  await expect(page.getByRole('region', { name: 'Conversation messages' })).toBeVisible();
  await page.getByRole('link', { name: /summary.*MemStation launch tracks/ }).click();
  await expect(page).toHaveURL(/\/w\/personal\/memory\/summary\/summary-1/);
  await page.locator('.workspace-tabs').getByRole('link', { name: 'Conversations', exact: true }).click();
  await page.getByLabel('Filter conversations').fill('conv-seed-2');
  await page.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(page.getByRole('link', { name: /conv-seed-1.*messages/ })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /conv-seed-2.*messages/ })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('workspace-conversations.png'), fullPage: true });
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 640 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    expect((await page.getByLabel('Filter conversations').boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.locator('.workspace-tabs').getByRole('link', { name: 'Memory documents', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Memory documents', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`workspace-memory-${width}.png`), fullPage: true });
    await page.locator('.workspace-tabs').getByRole('link', { name: 'Conversations', exact: true }).click();
    await expect(page.getByLabel('Filter conversations')).toBeVisible();
  }
});

test('workspace management rejects foreign records and mismatched browse links', async ({ page }) => {
  for (const path of ['/memory/summary/summary-1', '/conversations/conv-seed-1']) {
    await page.goto(`/t/fixture-target/w/personal-bob${path}`);
    await expect(page.getByRole('alert')).toContainText('unavailable in this workspace');
    await expect(page.getByText('MemStation launch tracks gateway rollout, memory verification, and admin observability.', { exact: true })).toHaveCount(0);
  }
  await page.goto('/t/fixture-target/w/personal/memory?user_id=bob');
  await expect(page.getByRole('alert')).toContainText('does not match');
  await page.goto('/t/fixture-target/w/personal-bob/memory');
  await expect(page.getByText('No memory documents match this view.')).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(0);
  await page.goto('/t/fixture-target/w/missing/conversations');
  await expect(page.getByRole('alert')).toContainText('no longer exists');
  await expect(page.getByText('No conversations match this view.')).toHaveCount(0);
});

test('workspace management browser reads ignore a late previous workspace', async ({ page }) => {
  await page.goto('/t/fixture-target/w/personal/settings');
  let release: (() => void) | undefined;
  let started: (() => void) | undefined;
  const pending = new Promise<void>(resolve => { started = resolve; });
  const held = new Promise<void>(resolve => { release = resolve; });
  await page.route('**/t/fixture-target/w/personal/memory/__data.json*', async route => {
    const response = await route.fetch(); started?.(); await held; await route.fulfill({ response });
  });
  await page.locator('.workspace-tabs').getByRole('link', { name: 'Memory documents', exact: true }).click({ noWaitAfter: true });
  await pending;
  await page.getByRole('link', { name: 'All workspaces', exact: true }).click();
  await page.getByRole('link', { name: /Bob notes/ }).click();
  await page.locator('.workspace-tabs').getByRole('link', { name: 'Memory documents', exact: true }).click();
  await expect(page.getByText('No memory documents match this view.')).toBeVisible();
  release?.();
  await page.waitForLoadState('networkidle');
  await expect(page.getByText('No memory documents match this view.')).toBeVisible();
  await expect(page.getByRole('link', { name: /summary.*MemStation launch/ })).toHaveCount(0);
});


test('workspace management edits memory with conflict review and restores scoped drafts', async ({ page }, testInfo) => {
  const route = '/t/fixture-target/w/personal/memory/summary/summary-1';
  await page.goto(route);
  await page.getByRole('button', { name: 'Edit memory', exact: true }).click();
  await page.getByLabel('Memory content', { exact: true }).fill('My local revision');
  const api = '/manager/api/targets/fixture-target/api/admin/workspaces/personal/memory/documents/summary/summary-1';
  const original = await (await page.request.get(api)).json();
  const remote = await page.request.put(api, { headers: { origin: new URL(page.url()).origin }, data: { content: 'Another editor revision', expected_updated_at: original.updated_at, generation_id: original.generation_id } });
  expect(remote.status()).toBe(200);
  await page.getByRole('button', { name: 'Save memory', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Your draft is preserved');
  await expect(page.getByRole('button', { name: 'Use draft against this version' })).toBeDisabled();
  await page.getByRole('button', { name: 'Load current version' }).click();
  await expect(page.getByText('Another editor revision', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Memory content', { exact: true })).toHaveValue('My local revision');
  await page.getByRole('button', { name: 'Use draft against this version' }).click();
  await page.getByRole('button', { name: 'Save memory', exact: true }).click();
  await expect(page.getByText('Memory saved.', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Edit memory', exact: true }).click();
  await page.getByLabel('Memory content', { exact: true }).fill('A retained draft');
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('link', { name: 'All workspace memory documents', exact: true }).click();
  await page.getByRole('link', { name: /My local revision.*summary-1/ }).click();
  await expect(page.getByLabel('Memory content', { exact: true })).toHaveValue('A retained draft');
  await page.reload();
  await expect(page.getByLabel('Memory content', { exact: true })).toHaveValue('A retained draft');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('workspace-memory-editor.png'), fullPage: true });
  await page.getByRole('button', { name: 'Discard draft' }).click();
  await expect(page.getByText('My local revision', { exact: true })).toBeVisible();
});

test('workspace management key memory create update stale delete and mobile controls', async ({ page }, testInfo) => {
  const base = '/t/fixture-target/w/personal';
  await page.goto(base + '/keys');
  await page.getByRole('link', { name: 'New key memory', exact: true }).click();
  await page.getByLabel('Key', { exact: true }).fill('project.instructions');
  await page.getByLabel('Value', { exact: true }).fill('Keep all workspace scopes explicit');
  await page.getByLabel('Category', { exact: true }).fill('project');
  await page.getByRole('button', { name: 'Create key memory', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'project.instructions', exact: true })).toBeVisible();
  const detailURL = page.url();
  const id = new URL(detailURL).pathname.split('/').at(-1);
  await page.getByLabel('Category', { exact: true }).fill('');
  await page.getByRole('button', { name: 'Save memory', exact: true }).click();
  await expect(page.getByText('Memory saved.', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Category', { exact: true })).toHaveValue('');
  for (const size of [{ width: 320, height: 640 }, { width: 844, height: 390 }]) {
    await page.setViewportSize(size);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    expect((await page.getByLabel('Category', { exact: true }).boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.getByRole('button', { name: 'Save memory', exact: true }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: testInfo.outputPath(`workspace-key-editor-${size.width}.png`), fullPage: false });
  }
  const api = `/manager/api/targets/fixture-target/api/admin/workspaces/personal/memory/keys/${id}`;
  const current = await (await page.request.get(api)).json();
  expect((await page.request.put(api, { headers: { origin: new URL(page.url()).origin }, data: { value: 'Remote protected value', category: '', review_status: 'approved', expected_updated_at: current.updated_at } })).ok()).toBeTruthy();
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Delete key memory', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('changed');
  expect((await page.request.get(api)).ok()).toBeTruthy();
  await page.getByRole('button', { name: 'Load current version' }).click();
  await expect(page.getByLabel('Value', { exact: true })).toHaveValue('Remote protected value');
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Delete key memory', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`${base}/keys$`));
  expect((await page.request.get(api)).status()).toBe(404);
});


test('workspace management paginates all memory and searches historical snapshots', async ({ page }, testInfo) => {
  await page.context().request.post(`${fixtureBaseUrl}/__test/browse-seed`);
  const base = '/t/fixture-target/w/personal';
  await page.goto(`${base}/memory?q=Paginated&kind=summary`);
  await expect(page.locator('.record-row')).toHaveCount(30);
  const firstIDs = await page.locator('.record-row').allTextContents();
  await page.getByRole('link', { name: 'Next documents', exact: true }).click();
  await expect(page).toHaveURL(/after=/);
  await expect(page.locator('.record-row')).toHaveCount(30);
  expect((await page.locator('.record-row').allTextContents()).some(item => firstIDs.includes(item))).toBeFalsy();
  await page.getByLabel('Filter documents').fill('Unique old archive');
  await page.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(page).not.toHaveURL(/after=/);
  await expect(page.getByRole('link', { name: /Unique old archive evidence.*old-filter-record/ })).toBeVisible();
  await page.goto(`${base}/memory/search?q=Gateway&kind=fact&known_at=2099-01-01T00%3A00%3A00Z`);
  await expect(page.getByText('Historical fact snapshots are read only.', { exact: false })).toBeVisible();
  await expect(page.locator('.search-result')).not.toHaveCount(0);
  await expect(page.getByRole('button', { name: /Edit memory|Save changes/ })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Open current record' }).first()).toHaveAttribute('href', /\/w\/personal\/memory\/fact\//);
  await page.reload();
  await expect(page.getByLabel('Known by the system at')).toHaveValue('2099-01-01T00:00:00Z');
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 640 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    expect((await page.getByLabel('Search memory', { exact: true }).boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.screenshot({ path: testInfo.outputPath(`workspace-history-${width}.png`), fullPage: true });
  }
  await page.getByRole('button', { name: 'Switch workspace: Personal' }).click();
  await page.getByLabel('Find a workspace').fill('Bob');
  await page.getByRole('dialog').getByRole('button', { name: 'Search', exact: true }).click();
  await page.getByRole('dialog').getByRole('link', { name: /Bob notes/ }).click();
  await expect(page).toHaveURL(/\/w\/personal-bob\/memory\/search\?/);
  await expect(page).toHaveURL(/known_at=/);
  await expect(page).not.toHaveURL(/generation_id=|after=/);
  await expect(page.getByText('No memories match these filters.')).toBeVisible();
});

test('workspace management picker respects dirty editors', async ({ page }) => {
  const base = '/t/fixture-target/w/personal';
  await page.goto(`${base}/memory/summary/summary-1?generation_id=generation-fixture`);
  await page.getByRole('button', { name: 'Edit memory', exact: true }).click();
  await page.getByLabel('Memory content').fill('Draft belongs only to Personal');
  await page.getByRole('button', { name: 'Switch workspace: Personal' }).click();
  await Promise.all([page.waitForEvent('dialog').then(dialog => dialog.dismiss()), page.getByRole('dialog').getByRole('link', { name: /Bob notes/ }).click()]);
  await expect(page).toHaveURL(/\/w\/personal\/memory\/summary/);
  await Promise.all([page.waitForEvent('dialog').then(dialog => dialog.accept()), page.getByRole('dialog').getByRole('link', { name: /Bob notes/ }).click()]);
  await expect(page).toHaveURL(/\/w\/personal-bob\/memory$/);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.goto(`${base}/memory/summary/summary-1`);
  await expect(page.getByLabel('Memory content')).toHaveValue('Draft belongs only to Personal');
});


test('workspace management picker ignores a closed request after reopening', async ({ page }) => {
  await page.goto('/t/fixture-target/w/personal/memory');
  let release: (() => void) | undefined;
  let started: (() => void) | undefined;
  const pending = new Promise<void>(resolve => { started = resolve; });
  const held = new Promise<void>(resolve => { release = resolve; });
  let first = true;
  await page.route('**/manager/api/targets/fixture-target/api/admin/workspaces?*', async route => {
    if (!first) { await route.continue(); return; }
    first = false;
    const response = await route.fetch(); started?.(); await held; await route.fulfill({ response });
  });
  await page.getByRole('button', { name: 'Switch workspace: Personal' }).click();
  await pending;
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Switch workspace: Personal' }).click();
  await expect(page.getByRole('dialog').getByRole('link', { name: /Personal Owner/ })).toBeVisible();
  await page.getByLabel('Find a workspace').fill('Bob');
  await page.getByRole('dialog').getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page.getByRole('dialog').getByRole('link', { name: /Personal Owner/ })).toHaveCount(0);
  release?.();
  await page.waitForLoadState('networkidle');
  await expect(page.getByRole('dialog').getByRole('link', { name: /Personal Owner/ })).toHaveCount(0);
  await expect(page.getByRole('dialog').getByRole('link', { name: /Bob notes/ })).toBeVisible();
  await page.getByLabel('Find a workspace').fill('Personal');
  await page.getByRole('dialog').getByRole('button', { name: 'Search', exact: true }).click();
  await page.getByRole('dialog').getByRole('link', { name: /Personal Owner/ }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
});


test('shared controls collapsible preserves native form fields, keyboard access and validation', async ({ page }, testInfo) => {
  const path = '/t/fixture-target/w/personal/memory/search?q=Gateway&kind=fact&known_at=2099-01-01T00%3A00%3A00Z';
  await page.goto(path);
  const trigger = page.getByRole('button', { name: 'Filters (2)', exact: true });
  const known = page.getByLabel('Known by the system at');
  const maximum = page.getByLabel('Maximum results');
  const content = page.locator('.disclosure-content');
  await expect(trigger).toHaveAttribute('aria-expanded', 'false');
  await expect(known).toBeHidden();
  await expect(content).toHaveAttribute('inert', '');
  expect(await page.locator('form').evaluate(form => Object.fromEntries(new FormData(form as HTMLFormElement)))).toMatchObject({ known_at: '2099-01-01T00:00:00Z', kind: 'fact', top_n: '10' });
  const controls = await trigger.getAttribute('aria-controls');
  expect(controls).toBeTruthy();
  expect(await content.getAttribute('id')).toBe(controls);
  await trigger.focus();
  await page.keyboard.press('Enter');
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');
  await expect(known).toBeVisible();
  expect(await content.evaluate(element => getComputedStyle(element).overflow)).toBe('hidden');
  await maximum.fill('7');
  await known.fill('2098-06-01T00:00:00Z');
  await trigger.focus();
  await page.keyboard.press('Space');
  await expect(known).toBeHidden();
  await page.keyboard.press('Tab');
  expect(await page.evaluate(() => document.activeElement?.closest('.disclosure-content') === null)).toBeTruthy();
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page).toHaveURL(/top_n=7/);
  await expect(page).toHaveURL(/known_at=2098-06-01/);
  await page.reload();
  await expect(trigger).toHaveAttribute('aria-expanded', 'false');
  await trigger.click();
  await expect(maximum).toHaveValue('7');
  await expect(known).toHaveValue('2098-06-01T00:00:00Z');
  await maximum.fill('0');
  await trigger.click();
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');
  await expect(maximum).toBeFocused();
  expect(await maximum.evaluate(field => (field as HTMLInputElement).validity.rangeUnderflow)).toBeTruthy();
  await maximum.fill('7');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await trigger.click();
  await expect(known).toBeHidden();
  expect(await content.evaluate(element => getComputedStyle(element).animationName)).toBe('none');
  for (const width of [1440, 320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    if (width <= 390) expect((await trigger.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.screenshot({ path: testInfo.outputPath(`disclosure-filter-closed-${width}.png`), fullPage: true, animations: 'disabled' });
    await trigger.click();
    await expect(known).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`disclosure-filter-open-${width}.png`), fullPage: true, animations: 'disabled' });
    await trigger.click();
  }
});

test('shared controls collapsible unifies workspace, agent and task details', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const [path, title, field] of [
    ['/t/fixture-target/w/personal/settings', 'Workspace details', 'Policy version'],
    ['/users/admin/agents/coding-agent', 'Identity details', 'Actor ID']
  ]) {
    await page.goto(path);
    const disclosure = page.locator('.disclosure').filter({ has: page.getByRole('button', { name: title, exact: true }) });
    const trigger = disclosure.getByRole('button', { name: title, exact: true });
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await trigger.focus();
    await page.keyboard.press('Enter');
    await expect(disclosure.getByText(field, { exact: true })).toBeVisible();
    expect((await trigger.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`disclosure-${title.replaceAll(' ', '-')}.png`), fullPage: true, animations: 'disabled' });
    expect(await page.locator('details').count()).toBe(0);
  }
  await page.goto('/operations/tasks');
  const panel = page.locator('.disclosure').filter({ has: page.getByRole('button', { name: 'Publish manual task', exact: true }) });
  const toggle = panel.getByRole('button', { name: 'Publish manual task', exact: true });
  await toggle.click();
  await panel.getByLabel('Conversation ID', { exact: true }).fill('preserved-task-draft');
  await toggle.click();
  await expect(panel.getByLabel('Conversation ID', { exact: true })).toBeHidden();
  await toggle.click();
  await expect(panel.getByLabel('Conversation ID', { exact: true })).toHaveValue('preserved-task-draft');
  await page.screenshot({ path: testInfo.outputPath('disclosure-task-panel.png'), fullPage: true, animations: 'disabled' });
});


test('workspace management paginates conversations and key memories across the full scope', async ({ page }, testInfo) => {
  await page.context().request.post(`${fixtureBaseUrl}/__test/record-pages`);
  const base = '/t/fixture-target/w/personal';
  for (const [section, label, next, old] of [
    ['conversations', 'Filter conversations', 'Next conversations', 'old-archive'],
    ['keys', 'Filter key memories', 'Next key memories', 'Unique old archive key']
  ]) {
    await page.goto(`${base}/${section}?q=page-`);
    await expect(page.locator('.record-row')).toHaveCount(30);
    const first = await page.locator('.record-row').allTextContents();
    await page.getByRole('link', { name: next, exact: true }).click();
    await expect(page).toHaveURL(/after=/);
    await expect(page.locator('.record-row')).toHaveCount(30);
    expect((await page.locator('.record-row').allTextContents()).some(item => first.includes(item))).toBeFalsy();
    await page.reload();
    await expect(page.getByLabel(label)).toHaveValue('page-');
    await page.getByLabel(label).fill('old-archive');
    await page.getByRole('button', { name: 'Apply', exact: true }).click();
    await expect(page).not.toHaveURL(/after=/);
    await expect(page.locator('.record-row')).toHaveCount(1);
    await expect(page.locator('.record-row')).toContainText(old);
    await page.goto(`${base}/${section}?q=page-&after=broken`);
    await expect(page.getByRole('alert')).toContainText('Invalid filters');
    await expect(page.getByLabel(label)).toHaveValue('page-');
    await page.getByRole('link', { name: 'First page', exact: true }).click();
    await expect(page.locator('.record-row')).toHaveCount(30);
    for (const width of [1440,320,390]) {
      await page.setViewportSize({ width, height: 844 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      expect((await page.getByRole('link', { name: next, exact: true }).boundingBox())!.height).toBeGreaterThanOrEqual(44);
      await page.screenshot({ path: testInfo.outputPath(`pagination-${section}-${width}.png`), fullPage: true, animations: 'disabled' });
    }
    await page.getByRole('link', { name: next, exact: true }).click();
    await page.getByRole('button', { name: 'Switch workspace: Personal' }).click();
    await page.getByLabel('Find a workspace').fill('Bob');
    await page.getByRole('dialog').getByRole('button', { name: 'Search', exact: true }).click();
    await page.getByRole('dialog').getByRole('link', { name: /Bob notes/ }).click();
    await expect(page).toHaveURL(new RegExp(`/w/personal-bob/${section}\\?q=page-`));
    await expect(page).not.toHaveURL(/after=/);
    await expect(page.locator('.record-row')).toHaveCount(0);
  }
});

test('workspace management navigation requires explicit scope and rejects retired inputs', async ({ page }) => {
  await page.goto('/t/fixture-target/workspaces?section=keys');
  await expect(page.getByText('Choose the workspace to browse.', { exact: false })).toBeVisible();
  await page.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(page).toHaveURL('/t/fixture-target/w/personal/keys');
  await page.goto('/t/fixture-target/workspaces?section=conversations&record=conv-seed-1&user_id=admin');
  await page.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(page).toHaveURL('/t/fixture-target/w/personal/conversations/conv-seed-1');
  await expect(page.getByRole('heading', { name: 'Conversation', exact: true })).toBeVisible();
  await page.goto('/t/fixture-target/workspaces?section=memory&kind=fact&record=fact-1&generation=generation-fixture&user_id=admin');
  await page.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(page).toHaveURL(/\/w\/personal\/memory\/fact\/fact-1\?generation_id=generation-fixture$/);
  await page.goto('/t/fixture-target/w/personal-bob/keys');
  await page.setViewportSize({ width: 1440, height: 900 });
  const nav = page.getByRole('navigation', { name: 'Primary navigation', exact: true });
  await expect(nav.getByRole('link', { name: 'Memory', exact: true })).toHaveAttribute('href', '/t/fixture-target/w/personal-bob/memory');
  await expect(nav.getByRole('link', { name: 'Conversations', exact: true })).toHaveAttribute('href', '/t/fixture-target/w/personal-bob/conversations');
  await page.goto('/users/admin');
  await page.getByRole('link', { name: 'Manage key memories', exact: true }).click();
  await expect(page).toHaveURL(/workspaces\?section=keys&user_id=admin$/);
  await page.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(page).toHaveURL('/t/fixture-target/w/personal/keys');
  const retired = await page.goto('/t/fixture-target/open/memory/keys');
  expect(retired?.status()).toBe(404);
  const obsolete = await page.goto('/memory/keys/key-1?user_id=admin&manager_target=fixture-target');
  expect(obsolete?.status()).toBe(400);
  await expect(page.getByText('Unsupported navigation parameter. Choose a service and workspace.')).toBeVisible();
});



test('workspace management picker anchors search, restores focus and preserves the current record', async ({ page }, testInfo) => {
  const path = '/t/fixture-target/w/personal/memory/summary/summary-1?generation_id=generation-fixture';
  await page.goto(path);
  const trigger = page.getByRole('button', { name: 'Switch workspace: Personal', exact: true });
  await trigger.focus();
  await page.keyboard.press('Enter');
  const picker = page.getByRole('dialog', { name: 'Switch workspace', exact: true });
  await expect(picker.getByLabel('Find a workspace')).toBeFocused();
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');
  await expect(page.locator('.dialog-overlay')).toHaveCount(0);
  await expect(picker.getByRole('link', { name: /Personal Owner: admin/ })).toHaveAttribute('aria-current', 'true');
  await page.keyboard.press('Escape');
  await expect(picker).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await trigger.click();
  await picker.getByRole('link', { name: /Personal Owner: admin/ }).click();
  await expect(picker).toHaveCount(0);
  await expect(page).toHaveURL(path);
  for (const [width, height] of [[1440, 900], [390, 640], [320, 480]]) {
    await page.setViewportSize({ width, height });
    await trigger.click();
    await expect(picker.getByRole('link', { name: /Bob notes/ })).toBeVisible();
    const box = (await picker.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(box.y + box.height).toBeLessThanOrEqual(height + 1);
    const input = (await picker.getByLabel('Find a workspace').boundingBox())!;
    const search = (await picker.getByRole('button', { name: 'Search', exact: true }).boundingBox())!;
    expect(Math.abs(input.y + input.height - search.y - search.height)).toBeLessThanOrEqual(1);
    if (width < 400) expect(input.height).toBeGreaterThanOrEqual(44);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`workspace-picker-${width}.png`), fullPage: true });
    await page.keyboard.press('Escape');
  }
  await page.goto('/t/fixture-target/w/personal/keys?q=preference');
  await expect(page.locator('.key-filter .input')).toBeVisible();
  const apply = (await page.getByRole('button', { name: 'Apply', exact: true }).boundingBox())!;
  const create = (await page.getByRole('link', { name: 'New key memory', exact: true }).boundingBox())!;
  expect(Math.abs(apply.y - create.y)).toBeLessThanOrEqual(1);
  await page.screenshot({ path: testInfo.outputPath('workspace-key-toolbar-320.png'), fullPage: true });
});

test('workspace management picker recovers errors and wraps long workspace names', async ({ page }, testInfo) => {
  await page.goto('/t/fixture-target/w/personal/memory');
  let fail = true;
  await page.route('**/manager/api/targets/fixture-target/api/admin/workspaces?*', async route => {
    if (fail) { fail = false; await route.fulfill({ status: 503, json: { error: 'Workspace directory is temporarily unavailable.' } }); return; }
    const response = await route.fetch();
    const data = await response.json();
    data.items = data.items.map((item: {id: string; name: string}) => item.id === 'personal-bob' ? { ...item, name: 'A very long workspace name for research and engineering notes with uninterruptedidentifier01234567890123456789' } : item);
    await route.fulfill({ response, json: data });
  });
  await page.setViewportSize({ width: 320, height: 480 });
  await page.getByRole('button', { name: 'Switch workspace: Personal', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Switch workspace', exact: true });
  await expect(picker.getByRole('alert')).toBeVisible();
  await picker.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(picker.getByRole('link', { name: /A very long workspace name/ })).toBeVisible();
  expect(await picker.evaluate(element => element.scrollWidth <= element.clientWidth)).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('workspace-picker-retry-long-name-320.png'), fullPage: true });
  await picker.getByRole('link', { name: /A very long workspace name/ }).click();
  await expect(page).toHaveURL('/t/fixture-target/w/personal-bob/memory');
});


test('workspace management related Key Memory opens its scoped editor without a generation', async ({ page }) => {
  const base = '/t/fixture-target/w/personal';
  await page.goto(base + '/memory/summary/summary-1?generation_id=generation-admin-active');
  const link = page.getByRole('link', { name: /context.*key_memory.*key-1/ });
  await expect(link).toHaveAttribute('href', base + '/keys/key-1');
  await link.click();
  await expect(page).toHaveURL(base + '/keys/key-1');
  await expect(page.getByRole('heading', { name: 'rollout_owner', exact: true })).toBeVisible();
  await expect(page.getByLabel('Value', { exact: true })).toHaveValue('ops-team');
  await expect(page.getByRole('button', { name: 'Save memory', exact: true })).toBeDisabled();
});


test('mobile settings rerank provider preserves model namespaces and can disable ranking', async ({ page }) => {
  await page.setViewportSize({width:320,height:568});
  await page.goto('/settings/system');
  await expect(page.getByLabel('Score policy',{exact:true})).toHaveCount(0);
  await expect(page.getByLabel('Rerank blend weight',{exact:true})).toHaveCount(0);
  await chooseBitsSelectOption(page, 'Rerank provider', 'OpenRouter');
  await page.getByLabel('Rerank model').fill('vendor/model:tag');
  await page.getByRole('button', { name: 'Save System Settings' }).click();
  await expect(page.getByText('System settings applied to the target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Rerank provider')).toContainText('OpenRouter');
  await expect(page.getByLabel('Rerank model')).toHaveValue('vendor/model:tag');
  await chooseBitsSelectOption(page, 'Rerank provider', 'Local / compatible rerank API');
  await page.getByLabel('Rerank model').fill('local-ranker');
  await page.getByRole('button', { name: 'Save System Settings' }).click();
  await expect(page.getByText('System settings applied to the target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Rerank provider')).toContainText('Local / compatible rerank API');
  await expect(page.getByLabel('Rerank model')).toHaveValue('local-ranker');
  await chooseBitsSelectOption(page, 'Rerank provider', 'Disabled');
  await expect(page.getByLabel('Rerank model')).toBeDisabled();
  await page.getByRole('button', { name: 'Save System Settings' }).click();
  await expect(page.getByText('System settings applied to the target.')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Rerank provider')).toContainText('Disabled');
});


test('workspace management memory service reviews evidence, publishes and erases with tracked progress', async ({ page }, testInfo) => {
  const base = '/t/fixture-target/w/personal/lifecycle';
  const created = await page.request.post('/manager/api/targets/fixture-target/api/admin/workspaces', {
    headers: { origin: new URL(page.url()).origin }, data: { user_id: 'admin', name: 'Team notes', kind: 'group' }
  });
  expect(created.ok()).toBeTruthy();
  await page.goto(base);
  const sections = page.getByRole('navigation', { name: 'Memory service sections', exact: true });
  await expect(page.getByRole('heading', { name: 'Message processing', exact: true })).toBeVisible();
  await expect(page.getByRole('progressbar')).toHaveAttribute('value', '4');
  await expect(page.getByRole('progressbar')).toHaveAttribute('max', '7');
  await expect(page.getByRole('button', { name: 'Save connection', exact: true })).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('memory-service-activity.png'), fullPage: true });
  await page.getByRole('button', { name: 'Retry task', exact: true }).click();
  await expect(page.getByText('Task queued for retry.', { exact: true })).toBeVisible();
  await sections.getByRole('link', { name: /^Review/ }).click();
  await page.getByRole('button', { name: 'View evidence', exact: true }).click();
  const source = page.getByRole('dialog', { name: 'Source message', exact: true });
  await expect(source.getByText('我喜欢安静的工作环境。', { exact: true })).toBeVisible();
  await expect(source.getByText('workspace-diagram.png', { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('source-media-metadata.png'), fullPage: true });
  await source.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Approve', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Nothing waiting for review', exact: true })).toBeVisible();
  await chooseBitsSelectOption(page, 'Show suggestions', 'Approved');
  await page.getByRole('button', { name: 'Open memory', exact: true }).click();
  const memory = page.getByRole('dialog');
  await expect(memory.getByRole('button', { name: 'Suggest correction', exact: true })).toBeEnabled();
  await memory.getByRole('button', { name: 'Share a copy', exact: true }).click();
  await chooseBitsSelectOption(page, 'Target workspace', 'Team notes');
  await memory.getByRole('button', { name: 'Preview copy', exact: true }).click();
  await expect(memory.getByRole('heading', { name: 'Confirm shared copy', exact: true })).toBeVisible();
  await memory.getByRole('button', { name: 'Publish copy', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Shared copies', exact: true })).toBeVisible();
  await expect(page.getByText('Team notes', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Retract', exact: true }).click();
  await expect(page.getByText('Retracted', { exact: true })).toBeVisible();
  await sections.getByRole('link', { name: 'Data', exact: true }).click();
  await page.getByRole('button', { name: 'Search index and AI settings', exact: true }).click();
  await page.getByRole('button', { name: 'Rebuild index', exact: true }).click();
  await expect(page.getByText('0 of 2 processed', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Discard index', exact: true }).click();
  await expect(page.getByText('Unused index discarded.', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Delete data', exact: true }).click();
  await page.getByLabel('Source message ID', { exact: true }).fill('source-1');
  await page.getByLabel('Confirm workspace name', { exact: true }).fill('Personal');
  await page.getByRole('button', { name: 'Delete selected data', exact: true }).click();
  const deletion = page.getByRole('dialog', { name: 'Deletion status', exact: true });
  await expect(deletion.getByText('Access blocked', { exact: true })).toBeVisible();
  await expect(deletion.getByText('Deletion history saved', { exact: true })).toBeVisible();
  await expect(deletion.getByText('Done', { exact: true })).toHaveCount(3);
  await deletion.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Create export', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Download export', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('memory-service-deletion-export.png'), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
});

test('workspace management memory service preserves drafts across sections and revision conflicts', async ({ page }, testInfo) => {
  await page.goto('/t/fixture-target/w/personal/lifecycle?view=review');
  const sections = page.getByRole('navigation', { name: 'Memory service sections', exact: true });
  await page.getByRole('button', { name: 'Find memory', exact: true }).click();
  await page.getByLabel('Find memory', { exact: true }).fill('安静');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await page.getByRole('button', { name: 'Select memory', exact: true }).click();
  await page.getByRole('button', { name: 'Suggest correction', exact: true }).click();
  await page.getByLabel('Suggestion', { exact: true }).fill('小林现在喜欢协作办公。');
  await page.getByRole('button', { name: 'Keep draft', exact: true }).click();
  await sections.getByRole('link', { name: 'Connections', exact: true }).click();
  await sections.getByRole('link', { name: /^Review/ }).click();
  await page.getByRole('button', { name: 'Resume suggestion', exact: true }).click();
  await expect(page.getByLabel('Suggestion', { exact: true })).toHaveValue('小林现在喜欢协作办公。');
  const lifecycle = '/manager/api/targets/fixture-target/api/admin/workspaces/personal/lifecycle';
  const headers = { origin: new URL(page.url()).origin };
  const competing = await page.request.post(lifecycle + '/memory/candidates', { headers, data: { kind: 'preference', content: '小林最近在远程办公。', target_node_id: 'node-1', target_revision: 1, source_refs: [], subject_actor_ids: ['actor-lin'] } });
  expect(competing.ok()).toBeTruthy();
  const competingID = (await competing.json()).id;
  const approved = await page.request.post(lifecycle + `/memory/candidates/${competingID}/decision`, { headers, data: { expected_revision: 1, decision: 'approve' } });
  expect(approved.ok()).toBeTruthy();
  await page.getByRole('button', { name: 'Submit suggestion', exact: true }).click();
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('Your draft is safe');
  await expect(page.getByLabel('Suggestion', { exact: true })).toHaveValue('小林现在喜欢协作办公。');
  await page.getByRole('button', { name: 'Load current memory', exact: true }).click();
  await expect(page.getByRole('dialog').getByText('小林最近在远程办公。', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Use revision 2', exact: true }).click();
  await expect(page.getByLabel('Suggestion', { exact: true })).toHaveValue('小林现在喜欢协作办公。');
  await page.screenshot({ path: testInfo.outputPath('memory-service-correction-conflict.png'), fullPage: true });
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click();
  page.once('dialog', dialog => dialog.dismiss());
  await page.getByRole('navigation', { name: 'Workspace sections', exact: true }).getByRole('link', { name: 'Memory documents', exact: true }).click();
  await expect(page).toHaveURL(/lifecycle/);
  await page.getByRole('button', { name: 'Resume suggestion', exact: true }).click();
  await page.getByRole('button', { name: 'Submit suggestion', exact: true }).click();
  await expect(page.getByText('Suggestion sent for review.', { exact: true })).toBeVisible();
  await expect(page.getByText('小林现在喜欢协作办公。', { exact: true })).toBeVisible();
});

test('workspace management memory service connections and private recipients fit short mobile dialogs', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 375, height: 480 });
  await page.goto('/t/fixture-target/w/personal/lifecycle?view=connections');
  await page.getByRole('button', { name: 'Connect source', exact: true }).click();
  await page.getByLabel('Connection name', { exact: true }).fill('Design team');
  await chooseBitsSelectOption(page, 'Observing Agent', 'Coding agent');
  await page.getByLabel('Source space ID', { exact: true }).fill('chat-123');
  const dialog = page.getByRole('dialog');
  const footer = dialog.getByRole('button', { name: 'Save connection', exact: true });
  const box = await footer.boundingBox();
  expect(box && box.y >= 0 && box.y + box.height <= 480).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('memory-service-connection-375x480.png'), fullPage: true });
  await footer.click();
  await expect(page.getByText('Design team', { exact: true })).toBeVisible();
  await expect(page.getByText('Coding agent', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Edit connection', exact: true }).click();
  await page.getByRole('button', { name: 'Revoke ingestion', exact: true }).click();
  await expect(page.getByText('Ingestion revoked', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Edit connection', exact: true }).click();
  await page.getByRole('checkbox', { name: 'Receive message events', exact: true }).check();
  await page.getByRole('button', { name: 'Save connection', exact: true }).click();
  await expect(page.getByText('Receiving messages', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Create private audience', exact: true }).click();
  await chooseBitsSelectOption(page, 'Authorized Agent', 'Coding agent');
  await expect(page.getByRole('button', { name: 'Save audience', exact: true })).toBeDisabled();
  await page.getByRole('checkbox', { name: '小林', exact: true }).check();
  await page.getByRole('button', { name: 'Save audience', exact: true }).click();
  await expect(page.getByText('Private', { exact: true })).toBeVisible();
  await chooseBitsSelectOption(page, 'Audience', 'Private: 小林');
  await page.getByRole('button', { name: 'Apply audience', exact: true }).click();
  await expect(page).toHaveURL(/partition_id=partitions-0/);
  await expect(page.getByText('Private view. Access is audited.', { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('memory-service-connections-private.png'), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
});


test('workspace management modern navigation and activity stay usable across screen sizes', async ({ page }, testInfo) => {
  const base = '/t/fixture-target/w/personal/lifecycle';
  const currentSectionFits = async (name: string) => {
    const nav = page.getByRole('navigation', { name, exact: true });
    const current = nav.locator('[aria-current="page"]');
    const [container, item] = await Promise.all([nav.boundingBox(), current.boundingBox()]);
    return Boolean(container && item && item.x >= container.x - 1 && item.x + item.width <= container.x + container.width + 1);
  };
  for (const width of [1440, 1024, 768, 390, 320]) {
    await page.setViewportSize({ width, height: width < 640 ? 740 : 960 });
    if (width !== 1440) await expect.poll(() => currentSectionFits('Workspace sections')).toBeTruthy();
    await page.goto(base);
    await expect(page.getByRole('heading', { name: 'Message processing', exact: true })).toBeVisible();
    const sections = page.getByRole('navigation', { name: 'Memory service sections', exact: true });
    await expect(sections.getByRole('link', { name: 'Activity', exact: true })).toHaveAttribute('aria-current', 'page');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    const topbar = await page.locator('.app-topbar').boundingBox();
    expect(topbar && topbar.x > 0 && topbar.y > 0 && topbar.x + topbar.width < width).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`memory-modern-${width}.png`), fullPage: true });
    await page.getByRole('complementary', { name: 'Needs attention', exact: true }).getByRole('link', { name: /Failed tasks/ }).click();
    const heading = await page.getByRole('heading', { name: 'Background tasks', exact: true }).boundingBox();
    const toolbar = await page.locator('.app-topbar').boundingBox();
    expect(heading && toolbar && heading.y >= toolbar.y + toolbar.height).toBeTruthy();
    await expect(page.getByRole('button', { name: 'Retry task', exact: true })).toBeVisible();
    await sections.getByRole('link', { name: /^Review/ }).click();
    await expect(page.getByRole('button', { name: 'Approve', exact: true })).toBeVisible();
  }
  await page.setViewportSize({ width: 1440, height: 960 });
  for (const [view, heading] of [['review', 'Review suggestions'], ['sharing', 'Shared copies'], ['connections', 'Message sources'], ['data', 'Data controls']]) {
    await page.goto(base + '?view=' + view);
    await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`memory-modern-${view}.png`), fullPage: true });
  }
  await page.setViewportSize({ width: 320, height: 740 });
  await expect.poll(() => currentSectionFits('Memory service sections')).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('memory-modern-narrow-data.png'), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: 'Dashboard', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('modern-dashboard.png'), fullPage: true });
});


test('system section navigation stays below sticky controls while scrolling and resizing', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/settings/system');
  const navigation = page.getByRole('navigation', { name: 'System sections', exact: true });
  await navigation.getByRole('link', { name: 'Multimodal', exact: true }).click();
  await expectSystemAnchor(page, 'system-multimodal');
  const stickyGeometry = () => page.evaluate(() => {
    const toolbar = document.querySelector('.app-topbar')!.getBoundingClientRect();
    const header = document.querySelector('.page-header.is-sticky')!.getBoundingClientRect();
    const navigation = document.querySelector('.system-section-nav')!.getBoundingClientRect();
    const point = document.elementFromPoint(header.right - 80, (toolbar.bottom + header.top) / 2);
    return { toolbarTop: toolbar.top, toolbarBottom: toolbar.bottom, headerTop: header.top, headerBottom: header.bottom, navigationTop: navigation.top, navigationBottom: navigation.bottom, coveredGap: Boolean(point?.closest('.page-header.is-sticky')) };
  });
  const geometry = [{ phase: 'anchor', ...await stickyGeometry() }];
  await page.screenshot({ path: testInfo.outputPath('system-scroll-anchor.png'), fullPage: false });
  await expect.poll(async () => { const box = await stickyGeometry(); return box.navigationTop - box.headerBottom; }).toBeGreaterThanOrEqual(8);
  expect((await stickyGeometry()).coveredGap).toBeTruthy();
  const multimodal = page.getByRole('heading', { name: 'Multimodal', exact: true });
  expect((await multimodal.boundingBox())!.y).toBeGreaterThanOrEqual((await stickyGeometry()).headerBottom + 8);
  const targetScroll = await page.evaluate(() => Math.min(scrollY + 400, document.documentElement.scrollHeight - innerHeight));
  if (testInfo.project.name === 'mobile-webkit') await page.evaluate(() => window.scrollBy(0, 400));
  else { await page.mouse.move(1000, 450); await page.mouse.wheel(0, 400); }
  await expect.poll(() => page.evaluate(() => scrollY)).toBeCloseTo(targetScroll, 0);
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  await expect.poll(async () => (await stickyGeometry()).toolbarTop).toBeCloseTo(12, 0);
  await expect.poll(async () => { const box = await stickyGeometry(); return box.navigationTop - box.headerBottom; }).toBeGreaterThanOrEqual(8);
  expect((await stickyGeometry()).coveredGap).toBeTruthy();
  geometry.push({ phase: 'scroll', ...await stickyGeometry() });
  await page.screenshot({ path: testInfo.outputPath('system-scroll-wheel.png'), fullPage: false });
  // A taller title/status row must update both the sticky navigation and anchor offset.
  await page.addStyleTag({ content: '.page-header.is-sticky .page-title { font-size: 42px; } .page-header.is-sticky .page-context { font-size: 22px; }' });
  await expect.poll(async () => { const box = await stickyGeometry(); return box.navigationTop - box.headerBottom; }).toBeGreaterThanOrEqual(8);
  await navigation.getByRole('link', { name: 'Web', exact: true }).click();
  await expectSystemAnchor(page, 'system-web');
  expect((await page.getByRole('heading', { name: 'Web', exact: true }).boundingBox())!.y).toBeGreaterThanOrEqual((await stickyGeometry()).headerBottom + 8);
  geometry.push({ phase: 'larger text', ...await stickyGeometry() });
  await page.setViewportSize({ width: 1100, height: 400 });
  await expect.poll(async () => (await stickyGeometry()).navigationBottom).toBeLessThanOrEqual(390);
  await navigation.getByRole('link', { name: 'Overrides & drift', exact: true }).click();
  await expectSystemAnchor(page, 'system-overrides');
  await expect(page.getByRole('heading', { name: 'Overrides & drift', exact: true })).toBeInViewport();
  expect((await page.getByRole('heading', { name: 'Overrides & drift', exact: true }).boundingBox())!.y).toBeGreaterThanOrEqual((await stickyGeometry()).headerBottom);
  await expect.poll(async () => { const box = await stickyGeometry(); return box.navigationTop - box.headerBottom; }).toBeGreaterThanOrEqual(8);
  geometry.push({ phase: 'short screen', ...await stickyGeometry() });
  await page.screenshot({ path: testInfo.outputPath('system-scroll-short-screen.png'), fullPage: false });
  await mkdir(testInfo.outputDir, { recursive: true });
  await writeFile(testInfo.outputPath('scroll-geometry.json'), JSON.stringify(geometry, null, 2));
});


test('system section navigation follows visible modules with aligned links and scroll masks', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/settings/system');
  const navigation = page.getByRole('navigation', { name: 'System sections', exact: true });
  const current = navigation.locator('[aria-current="location"]');
  await expect(current).toHaveText('Traffic');
  expect(await navigation.getByRole('link', { name: 'Database connection', exact: true }).evaluate(element => ({ align: getComputedStyle(element).textAlign, justify: getComputedStyle(element).justifyContent }))).toEqual({ align: 'left', justify: 'flex-start' });
  await navigation.getByRole('link', { name: 'Relations', exact: true }).click();
  await expect(current).toHaveText('Relations');
  await expect(page).toHaveURL(/#system-relations$/);
  // Move the document without selecting a link: the current module must follow the viewport, not the hash.
  const target = await page.evaluate(() => {
    const section = document.getElementById('system-web')!;
    return scrollY + section.getBoundingClientRect().top - Number.parseFloat(getComputedStyle(section).scrollMarginTop) + 4;
  });
  if (testInfo.project.name === 'mobile-webkit') await page.evaluate(y => window.scrollTo(0, y), target);
  else {
    const delta = await page.evaluate(y => y - scrollY, target);
    await page.mouse.move(1100, 500);
    await page.mouse.wheel(0, delta);
  }
  await expect(current).toHaveText('Web');
  await expect(page).toHaveURL(/#system-relations$/);
  const mask = page.locator('.settings-scroll-mask');
  await expect(mask).toHaveCSS('pointer-events', 'none');
  expect(await mask.evaluate(element => getComputedStyle(element).backgroundImage)).toContain('linear-gradient');
  const [maskBox, headerBox] = await Promise.all([mask.boundingBox(), page.locator('.page-header.is-sticky').boundingBox()]);
  expect(maskBox && headerBox && Math.abs(maskBox.y - headerBox.y - headerBox.height) <= 1).toBeTruthy();
  await page.screenshot({ path: testInfo.outputPath('system-toc-web.png'), fullPage: false });
  await page.setViewportSize({ width: 1100, height: 400 });
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await expect(current).toHaveText('Overrides & drift');
  const activeFits = () => current.evaluate(element => {
    const selected = element.getBoundingClientRect();
    const bounds = element.parentElement!.getBoundingClientRect();
    return selected.left >= bounds.left - 1 && selected.right <= bounds.right + 1 && selected.top >= bounds.top - 1 && selected.bottom <= bounds.bottom + 1;
  });
  await expect.poll(activeFits).toBeTruthy();
  await expect(navigation).toHaveAttribute('data-mask-start', 'true');
  await expect(navigation).toHaveAttribute('data-mask-end', 'false');
  const position = await page.evaluate(() => scrollY);
  await navigation.evaluate(element => element.scrollTo({ top: 0 }));
  await expect(navigation).toHaveAttribute('data-mask-start', 'false');
  await expect(navigation).toHaveAttribute('data-mask-end', 'true');
  expect(await page.evaluate(() => scrollY)).toBe(position);
  await page.screenshot({ path: testInfo.outputPath('system-toc-short-mask.png'), fullPage: false });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/settings/system');
  await expect(current).toHaveText('Traffic');
  await navigation.getByRole('link', { name: 'Multimodal', exact: true }).click();
  await expect(current).toHaveText('Multimodal');
  await expect.poll(activeFits).toBeTruthy();
  expect(await navigation.evaluate(element => getComputedStyle(element).maskImage)).toContain('to right');
  await page.screenshot({ path: testInfo.outputPath('system-toc-mobile.png'), fullPage: false });
});

test('mobile navigation and desktop sidebar keep compact groups and a stable brand while scrolling', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto('/settings/system');
  const sidebar = page.locator('.app-sidebar');
  const brand = sidebar.locator('.sidebar-brand');
  const navigation = sidebar.getByRole('navigation', { name: 'Primary navigation', exact: true });
  const brandHeight = (await brand.boundingBox())!.height;
  const checkSpacing = async () => {
    const measurements = await sidebar.evaluate(element => {
      const nav = element.querySelector('.sidebar-nav')!;
      const sections = Array.from(nav.querySelectorAll('.sidebar-section'));
      const bounds = (element: Element) => element.getBoundingClientRect();
      return {
        groupGaps: sections.slice(1).map((section, index) => bounds(section).top - bounds(sections[index]).bottom),
        topPadding: bounds(sections[0]).top - bounds(nav).top + nav.scrollTop,
        brandGap: bounds(nav).top - bounds(element.querySelector('.sidebar-brand')!).bottom
      };
    });
    for (const gap of measurements.groupGaps) { expect(gap).toBeGreaterThanOrEqual(8); expect(gap).toBeLessThanOrEqual(16); }
    expect(measurements.topPadding).toBeLessThanOrEqual(12);
    expect(measurements.brandGap).toBe(0);
    return measurements;
  };
  const geometry = [{ phase: 'desktop', ...await checkSpacing() }];
  await expect(navigation).toHaveAttribute('data-scroll-start', 'false');
  await expect(navigation).toHaveAttribute('data-scroll-end', 'false');
  await page.screenshot({ path: testInfo.outputPath('sidebar-desktop.png') });
  await page.setViewportSize({ width: 1100, height: 480 });
  await expect.poll(async () => (await brand.boundingBox())!.height).toBe(brandHeight);
  await expect(navigation).toHaveAttribute('data-scroll-end', 'true');
  const bodyPosition = await page.evaluate(() => scrollY);
  await navigation.getByRole('link', { name: 'Providers', exact: true }).scrollIntoViewIfNeeded();
  await expect(navigation).toHaveAttribute('data-scroll-start', 'true');
  await expect(navigation.getByRole('link', { name: 'Providers', exact: true })).toBeInViewport();
  expect(await page.evaluate(() => scrollY)).toBe(bodyPosition);
  geometry.push({ phase: 'short desktop', ...await checkSpacing() });
  await page.screenshot({ path: testInfo.outputPath('sidebar-short.png') });
  await page.setViewportSize({ width: 390, height: 480 });
  await page.getByRole('button', { name: 'Open navigation', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Navigation', exact: true });
  await expect(drawer).toBeVisible();
  expect((await brand.boundingBox())!.height).toBe(brandHeight);
  geometry.push({ phase: 'short mobile', ...await checkSpacing() });
  const links = await navigation.getByRole('link').all();
  for (const link of links) expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await navigation.getByRole('link', { name: 'Providers', exact: true }).scrollIntoViewIfNeeded();
  await expect(navigation).toHaveAttribute('data-scroll-start', 'true');
  await page.screenshot({ path: testInfo.outputPath('sidebar-mobile.png') });
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
  await mkdir(testInfo.outputDir, { recursive: true });
  await writeFile(testInfo.outputPath('sidebar-spacing.json'), JSON.stringify(geometry, null, 2));
});

test('system section navigation uses smooth scrolling preferences and respects reduced motion', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/settings/system');
  const navigation = page.getByRole('navigation', { name: 'System sections', exact: true });
  const sheet = page.locator('.settings-sheet');
  const mask = sheet.locator('.settings-scroll-mask');
  await expect(sheet).toHaveCSS('overflow-x', 'clip');
  await expect(mask).toHaveCSS('opacity', '0');
  await page.screenshot({ path: testInfo.outputPath('system-surface-top.png') });
  await expect(page.locator('html')).toHaveCSS('scroll-behavior', 'smooth');
  const initialScroll = await page.evaluate(() => scrollY);
  await page.evaluate(() => {
    const samples: number[] = [];
    (window as Window & { systemScrollSamples?: number[] }).systemScrollSamples = samples;
    window.addEventListener('scroll', () => samples.push(scrollY), { passive: true });
  });
  await navigation.getByRole('link', { name: 'Multimodal', exact: true }).click();
  await expectSystemAnchor(page, 'system-multimodal');
  const samples = await page.evaluate(() => (window as Window & { systemScrollSamples?: number[] }).systemScrollSamples!);
  // Browsers can coalesce compositor scroll events under load. Verify the
  // declared motion policy and actual navigation; frame counts are diagnostic.
  expect(samples.some(value => Math.abs(value - initialScroll) > 50)).toBeTruthy();
  await expect(navigation.locator('[aria-current="location"]')).toHaveText('Multimodal');
  await expect(page.locator('#system-multimodal')).toBeFocused();
  await expect(mask).toHaveCSS('opacity', '1');
  expect((await mask.boundingBox())!.height).toBeGreaterThanOrEqual(32);
  const [maskBox, headerBox] = await Promise.all([mask.boundingBox(), page.locator('.page-header.is-sticky').boundingBox()]);
  expect(Math.abs(maskBox!.y - headerBox!.y - headerBox!.height)).toBeLessThanOrEqual(1);
  await page.screenshot({ path: testInfo.outputPath('system-surface-scrolled.png') });
  await navigation.getByRole('link', { name: 'Web', exact: true }).press('Enter');
  await expectSystemAnchor(page, 'system-web');
  await expect(page).toHaveURL(/#system-web$/);
  await expect(page.locator('#system-web')).toBeFocused();
  await page.evaluate(() => {
    window.scrollBy({ top: 220, behavior: 'instant' });
    (window as Window & { systemScrollSamples?: number[] }).systemScrollSamples!.length = 0;
  });
  const repeatedStart = await page.evaluate(() => scrollY);
  await navigation.getByRole('link', { name: 'Web', exact: true }).click();
  await expectSystemAnchor(page, 'system-web');
  await expect(page.locator('html')).toHaveCSS('scroll-behavior', 'smooth');
  const repeatedSamples = await page.evaluate(() => (window as Window & { systemScrollSamples?: number[] }).systemScrollSamples!);
  expect(repeatedSamples.some(value => Math.abs(value - repeatedStart) > 50)).toBeTruthy();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(page.locator('html')).toHaveCSS('scroll-behavior', 'auto');
  await navigation.getByRole('link', { name: 'Traffic', exact: true }).click();
  // Reduced motion must finish immediately, rather than passing after an animation timeout.
  const reducedDelta = await page.evaluate(() => new Promise<number>(resolve => requestAnimationFrame(() => {
    const section = document.getElementById('system-traffic')!;
    const target = Math.max(0, scrollY + section.getBoundingClientRect().top - Number.parseFloat(getComputedStyle(section).scrollMarginTop));
    resolve(Math.abs(scrollY - target));
  })));
  expect(reducedDelta).toBeLessThanOrEqual(2);
  await expect(mask).toHaveCSS('opacity', '0');
  await page.goBack();
  await expect(page).toHaveURL(/#system-web$/);
  await expectSystemAnchor(page, 'system-web');
  await mkdir(testInfo.outputDir, { recursive: true });
  await writeFile(testInfo.outputPath('smooth-scroll.json'), JSON.stringify({ samples, repeatedSamples, reducedDelta }, null, 2));
});

test('system section navigation keeps clicked short sections selected at the shared page end', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/settings/system');
  const navigation = page.getByRole('navigation', { name: 'System sections', exact: true });
  const current = navigation.locator('[aria-current="location"]');
  const database = navigation.getByRole('link', { name: 'Database connection', exact: true });
  const overrides = navigation.getByRole('link', { name: 'Overrides & drift', exact: true });
  await database.click();
  await expectSystemAnchor(page, 'system-database');
  await expect(page.getByRole('heading', { name: 'Database connection', exact: true })).toBeInViewport();
  await expect(page.getByRole('heading', { name: 'Overrides & drift', exact: true })).toBeInViewport();
  const position = await page.evaluate(() => scrollY);
  const remaining = await page.evaluate(() => document.documentElement.scrollHeight - innerHeight - scrollY);
  expect(remaining).toBeLessThanOrEqual(2);
  await page.screenshot({ path: testInfo.outputPath('database-selected.png') });
  await expect(current).toHaveText('Database connection');
  await page.locator('.settings-sheet').dispatchEvent('wheel', { deltaY: 100, ctrlKey: true });
  await expect(current).toHaveText('Database connection');
  await overrides.click();
  await expect(current).toHaveText('Overrides & drift');
  expect(Math.abs(await page.evaluate(() => scrollY) - position)).toBeLessThanOrEqual(2);
  await database.press('Enter');
  await expect(current).toHaveText('Database connection');
  expect(Math.abs(await page.evaluate(() => scrollY) - position)).toBeLessThanOrEqual(2);
  // A real scrolling key releases the explicit selection even at the document boundary.
  await page.locator('#system-database').press('PageDown');
  await expect(current).toHaveText('Overrides & drift');
  await database.click();
  await expect(current).toHaveText('Database connection');
  // Manual movement away and back must follow the viewport, despite the unchanged database hash.
  if (testInfo.project.name === 'mobile-webkit') await page.keyboard.press('PageUp');
  else { await page.mouse.move(1000, 450); await page.mouse.wheel(0, -450); }
  await expect.poll(() => page.evaluate(() => scrollY)).toBeLessThan(position - 100);
  await page.evaluate(() => new Promise<void>(resolve => {
    let last = scrollY;
    let stable = 0;
    const check = () => {
      stable = Math.abs(scrollY - last) < 0.5 ? stable + 1 : 0;
      last = scrollY;
      if (stable >= 3) resolve();
      else requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  }));
  await expect(current).not.toHaveText('Database connection');
  await page.evaluate(() => window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'instant' }));
  await expect(current).toHaveText('Overrides & drift');
  await expect(page).toHaveURL(/#system-database$/);
  await overrides.click();
  await expectSystemAnchor(page, 'system-overrides');
  await database.click();
  await expectSystemAnchor(page, 'system-database');
  await expect(current).toHaveText('Database connection');
  await page.goBack();
  await expect(page).toHaveURL(/#system-overrides$/);
  await expect(current).toHaveText('Overrides & drift');
  await page.goForward();
  await expect(page).toHaveURL(/#system-database$/);
  await expect(current).toHaveText('Database connection');
  await page.setViewportSize({ width: 1440, height: 1100 });
  await expect(current).toHaveText('Database connection');
  await page.setViewportSize({ width: 390, height: 1100 });
  await page.goto('/settings/system#system-database');
  await expectSystemAnchor(page, 'system-database');
  await expect(current).toHaveText('Database connection');
  await overrides.click();
  await expectSystemAnchor(page, 'system-overrides');
  await expect(current).toHaveText('Overrides & drift');
  await database.click();
  await expectSystemAnchor(page, 'system-database');
  await expect(current).toHaveText('Database connection');
});
