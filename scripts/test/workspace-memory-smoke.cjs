// Run ONLY against a disposable, migrated Go service with workspace-edit fixture
// owners. Mutates those fixtures; retains audited records for inspection.
// Manager runs from the production build; this script owns its Manager process.
const { createRequire } = require('node:module');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { randomBytes, randomUUID } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../..');
const req = createRequire(path.join(root, 'apps/manager/package.json'));
const { chromium, webkit, expect } = req('@playwright/test');
function required(name) { if (!process.env[name]) throw new Error(`${name} is required`); return process.env[name]; }
const target = required('MEMSTATION_TEST_TARGET_URL');
const token = required('MEMSTATION_TEST_ADMIN_TOKEN');
const redis = required('MEMSTATION_TEST_MANAGER_REDIS_URL');
const port = Number(process.env.MEMSTATION_TEST_MANAGER_PORT ?? 4274);
const origin = `http://127.0.0.1:${port}`;
const artifacts = path.resolve(process.env.MEMSTATION_TEST_ARTIFACT_DIR ?? path.join(root, '.artifacts/workspace-memory-smoke'));
const owner = `workspace-edit-run-${randomUUID()}`;
fs.mkdirSync(artifacts, { recursive: true });
const report = { node: process.version, productionBuild: true, owner, checks: [] };
const log = fs.openSync(path.join(artifacts, 'manager-runtime.log'), 'w');

(async () => {
  let child, browser, mobile;
  try {
    const probe = require('node:net').createServer();
    await new Promise((resolve, reject) => { probe.once('error', reject); probe.listen(port, '127.0.0.1', resolve); });
    await new Promise((resolve) => probe.close(resolve));
    child = spawn(process.execPath, ['server.mjs'], { cwd: path.join(root, 'apps/manager'), stdio: ['ignore', log, log], env: {
      ...process.env, NODE_ENV: 'production', HOST: '127.0.0.1', PORT: String(port), ORIGIN: origin,
      MEMSTATION_MANAGER_TARGETS: JSON.stringify([{ id: 'real', label: 'Local Go service', baseUrl: target }]),
      MEMSTATION_MANAGER_REDIS_URL: redis, MEMSTATION_MANAGER_SESSION_KEY: randomBytes(32).toString('hex')
    } });
    await expect.poll(async () => { try { return (await fetch(origin + '/login')).status; } catch { return 0; } }, { timeout: 20000 }).toBe(200);
    browser = await chromium.launch();
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    await page.goto(origin + '/login');
    await page.getByLabel('Connection URI or base URL').fill(target);
    await page.getByLabel('Label', { exact: true }).fill('Local Go service');
    await page.getByPlaceholder('MEMSTATION_ADMIN_TOKEN').fill(token);
    await page.getByRole('button', { name: 'Connect target', exact: true }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
    async function api(method, resource, data) {
      const response = await context.request.fetch(origin + '/manager/api/targets/real' + resource, { method, headers: { origin }, data });
      assert(response.ok(), `${method} ${resource}: ${response.status()}`);
      return response.json();
    }
    const [first, second] = JSON.parse(fs.readFileSync(required('MEMSTATION_TEST_WORKSPACE_EDIT_FIXTURES'), 'utf8'));
    assert(first?.workspace && second?.workspace && first.workspace !== second.workspace);
    assert(first.owner.startsWith('workspace-edit-'), 'Use the disposable workspace-edit fixture owner');
    const base = `/t/real/w/${first.workspace}`;
    const resource = `/api/admin/workspaces/${first.workspace}/memory`;
    const documentURL = `${base}/memory/summary/${first.document}?generation_id=${encodeURIComponent(first.generation)}`;
    const docPath = `${resource}/documents/summary/${first.document}`;
    await page.goto(origin + documentURL);
    await page.getByRole('button', { name: 'Edit memory', exact: true }).click();
    await page.getByLabel('Memory content', { exact: true }).fill('Local runtime draft with source evidence retained.');
    const before = await api('GET', docPath);
    await api('PUT', docPath, { content: 'Concurrent runtime revision', expected_updated_at: before.updated_at, generation_id: before.generation_id });
    await page.getByRole('button', { name: 'Save memory', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('Your draft is preserved');
    await page.getByRole('button', { name: 'Load current version' }).click();
    await expect(page.getByText('Concurrent runtime revision', { exact: true })).toBeVisible();
    await expect(page.getByLabel('Memory content', { exact: true })).toHaveValue('Local runtime draft with source evidence retained.');
    await page.screenshot({ path: path.join(artifacts, 'document-conflict-desktop.png'), fullPage: true });
    await page.getByRole('button', { name: 'Use draft against this version' }).click();
    await page.getByRole('button', { name: 'Save memory', exact: true }).click();
    await expect(page.getByText('Memory saved.', { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByText('Local runtime draft with source evidence retained.', { exact: true })).toBeVisible();
    const wrongScope = await context.request.put(origin + '/manager/api/targets/real' + `/api/admin/workspaces/${second.workspace}/memory/documents/summary/${first.document}`, { headers: { origin }, data: { content: 'wrong scope', generation_id: before.generation_id, expected_updated_at: before.updated_at } });
    assert.equal(wrongScope.status(), 404);
    report.checks.push('real PostgreSQL document edit, conflict preserves draft, explicit review/rebase, persisted reload, cross-workspace write 404');
    await page.goto(origin + base + '/keys/new');
    await page.getByLabel('Key', { exact: true }).fill('runtime.instructions');
    await page.getByLabel('Value', { exact: true }).fill('Keep source evidence and workspace boundaries explicit.');
    await page.getByLabel('Category', { exact: true }).fill('runtime');
    await page.getByRole('button', { name: 'Create key memory', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'runtime.instructions', exact: true })).toBeVisible();
    const keyURL = new URL(page.url()).pathname;
    const keyID = keyURL.split('/').at(-1);
    const duplicate = await context.request.post(origin + '/manager/api/targets/real' + resource + '/keys', { headers: { origin }, data: { key: 'runtime.instructions', value: 'overwrite', review_status: 'pending' } });
    assert.equal(duplicate.status(), 409);
    await page.getByLabel('Category', { exact: true }).fill('');
    await page.getByRole('button', { name: 'Save memory', exact: true }).click();
    await expect(page.getByText('Memory saved.', { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByLabel('Category', { exact: true })).toHaveValue('');
    mobile = await webkit.launch();
    for (const size of [{ width: 320, height: 640 }, { width: 390, height: 844 }, { width: 844, height: 390 }]) {
      const mobileContext = await mobile.newContext({ viewport: size, isMobile: true, deviceScaleFactor: 1, hasTouch: true, storageState: await context.storageState() });
      try {
        const mobilePage = await mobileContext.newPage();
        await mobilePage.goto(origin + keyURL, { waitUntil: 'domcontentloaded' });
        await expect(mobilePage.getByLabel('Value', { exact: true })).toHaveValue('Keep source evidence and workspace boundaries explicit.');
        assert(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
        assert((await mobilePage.getByLabel('Category', { exact: true }).boundingBox()).height >= 44);
        assert((await mobilePage.getByLabel('Value', { exact: true }).evaluate(el => parseFloat(getComputedStyle(el).fontSize))) >= 16);
        await mobilePage.screenshot({ path: path.join(artifacts, `key-editor-${size.width}.png`), fullPage: true });
        const save = mobilePage.getByRole('button', { name: 'Save memory', exact: true });
        await save.scrollIntoViewIfNeeded();
        const bounds = await save.boundingBox(); assert(bounds.y >= 0 && bounds.y + bounds.height <= size.height + 1);
        await mobilePage.screenshot({ path: path.join(artifacts, `key-editor-${size.width}-save.png`), fullPage: false });
      } finally { await mobileContext.close(); }
    }
    const keyPath = resource + '/keys/' + keyID;
    const current = await api('GET', keyPath);
    await api('PUT', keyPath, { value: 'Newer version must survive stale deletion.', category: '', review_status: 'approved', expected_updated_at: current.updated_at });
    page.once('dialog', dialog => dialog.accept());
    await page.getByRole('button', { name: 'Delete key memory', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('changed');
    await page.getByRole('button', { name: 'Load current version' }).click();
    await expect(page.getByLabel('Value', { exact: true })).toHaveValue('Newer version must survive stale deletion.');
    page.once('dialog', dialog => dialog.accept());
    await page.getByRole('button', { name: 'Delete key memory', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${base}/keys$`));
    const absent = await context.request.get(origin + '/manager/api/targets/real' + keyPath);
    assert.equal(absent.status(), 404);
    const audit = await api('GET', `/api/admin/audit?target_id=${keyID}`);
    assert.equal(audit.items.filter(item => item.action === 'create_key_memory').length, 1);
    assert.equal(audit.items.filter(item => item.action === 'delete_key_memory').length, 1);
    report.checks.push('real Key Memory create-only, category clear, persisted reload, stale delete conflict, reload then delete, exact create/delete audit counts');
    report.checks.push('production WebKit 320/390/844px: no horizontal overflow, 44px input, 16px text, save action visible in short landscape');
    const fresh = await browser.newContext();
    const response = await fresh.request.get(origin + documentURL);
    assert(!(await response.text()).includes('Local runtime draft with source evidence retained.'));
    await fresh.close();
    report.checks.push('anonymous SSR does not expose edited workspace content');
    report.ok = true;
  } finally {
    await browser?.close(); await mobile?.close();
    if (child && child.exitCode === null && child.signalCode === null) {
      const exited = once(child, 'exit'); child.kill('SIGTERM');
      const timer = setTimeout(() => child.kill('SIGKILL'), 8000); await exited; clearTimeout(timer);
      report.managerGracefulExit = child.signalCode !== 'SIGKILL';
    }
    fs.closeSync(log);
    fs.writeFileSync(path.join(artifacts, 'runtime-validation.json'), JSON.stringify(report, null, 2) + '\n');
  }
  console.log(JSON.stringify(report));
})().catch((error) => { console.error(error.message); process.exitCode = 1; });
