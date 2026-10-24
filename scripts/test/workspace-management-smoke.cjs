// Run against a disposable, migrated Go service. The script creates one unique
// owner and leaves its audited test records for inspection; it never edits an
// existing owner's policy. Manager itself runs from the production build.
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
const artifacts = path.resolve(process.env.MEMSTATION_TEST_ARTIFACT_DIR ?? path.join(root, '.artifacts/workspace-management-smoke'));
const owner = `workspace-smoke-${randomUUID()}`;
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
    const agent = await api('POST', `/api/admin/users/${owner}/agents`, { name: 'Workspace smoke reader', capabilities: ['memory.read'] });
    const issued = await api('POST', `/api/admin/users/${owner}/api-keys`, { name: 'Workspace smoke credential', agent_id: agent.id, capabilities: ['memory.read'] });
    const workspaceId = agent.default_workspace_id;
    const scopeURL = `/t/real/w/${workspaceId}/settings`;
    await page.goto(origin + scopeURL);
    await expect(page.getByLabel('Workspace name')).toHaveValue('Personal');
    await page.getByLabel('Workspace name').fill('Runtime policy check');
    await page.getByRole('checkbox', { name: /^Read memory/ }).uncheck();
    await page.getByRole('button', { name: 'Save workspace', exact: true }).click();
    await expect(page.getByText('Workspace saved.', { exact: false })).toBeVisible();
    const denied = await fetch(target + '/api/v1/memory/keys', { headers: { authorization: `Bearer ${issued.api_key}` } });
    assert.equal(denied.status, 403);
    const discovery = await fetch(target + '/api/v1/workspaces', { headers: { authorization: `Bearer ${issued.api_key}` } }).then((res) => res.json());
    assert.equal(discovery.items[0].name, 'Runtime policy check');
    assert.deepEqual(discovery.items[0].capabilities, []);
    await page.reload();
    await expect(page.getByLabel('Workspace name')).toHaveValue('Runtime policy check');
    await expect(page.getByRole('checkbox', { name: /^Read memory/ })).not.toBeChecked();
    await page.screenshot({ path: path.join(artifacts, 'desktop.png'), fullPage: true });
    const audit = await api('GET', `/api/admin/audit?action=update_workspace&target_id=${workspaceId}`);
    assert.equal(audit.items.length, 1);
    report.checks.push('real Go login, server-loaded workspace, policy update, persisted reload, one audit event and existing-key denial');
    await page.getByRole('link', { name: 'Agents with access', exact: true }).click();
    await page.getByRole('link', { name: /Workspace smoke reader/ }).click();
    await expect(page).toHaveURL(new RegExp(`/t/real/w/${workspaceId}/access/${agent.id}$`));
    const detail = await api('GET', `/api/admin/users/${owner}/agents/${agent.id}`);
    assert.deepEqual(detail.effective_capabilities, []);
    report.checks.push('workspace agent navigation preserves the real owner and workspace, effective permissions narrowed');
    mobile = await webkit.launch();
    const mobileContext = await mobile.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, deviceScaleFactor: 1, hasTouch: true });
    const connected = await mobileContext.request.post(origin + '/manager/session/real', { headers: { origin }, data: { token } });
    assert(connected.ok());
    const mobilePage = await mobileContext.newPage();
    for (const size of [{ width: 390, height: 844 }, { width: 320, height: 640 }, { width: 844, height: 390 }]) {
      await mobilePage.setViewportSize(size);
      await mobilePage.goto(origin + scopeURL);
      await expect(mobilePage.getByLabel('Workspace name')).toHaveValue('Runtime policy check');
      assert(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
      assert((await mobilePage.getByLabel('Workspace name').boundingBox()).height >= 44);
      await mobilePage.screenshot({ path: path.join(artifacts, `mobile-${size.width}.png`), fullPage: false });
      const save = mobilePage.getByRole('button', { name: 'Save workspace', exact: true });
      await save.scrollIntoViewIfNeeded();
      const saveBounds = await save.boundingBox();
      assert(saveBounds.y >= 0 && saveBounds.y + saveBounds.height <= size.height + 1);
      await mobilePage.screenshot({ path: path.join(artifacts, `mobile-${size.width}-save.png`), fullPage: false });
      await mobilePage.getByRole('link', { name: 'Agents with access', exact: true }).click();
      const tab = mobilePage.getByRole('link', { name: 'Agents with access', exact: true });
      const bounds = await tab.boundingBox();
      assert(bounds.x >= 0 && bounds.x + bounds.width <= size.width + 1);
    }
    report.checks.push('WebKit 390px, 320px and short landscape: no overflow, 44px inputs, active section visible');
    // Optional read-only fixtures produced in a disposable database. This also
    // exercises production SSR against Go instead of the browser fixture server.
    const browseFile = process.env.MEMSTATION_TEST_WORKSPACE_BROWSE_FIXTURES;
    if (browseFile) {
      const [first, second] = JSON.parse(fs.readFileSync(browseFile, 'utf8'));
      assert(first?.workspace && second?.workspace && first.workspace !== second.workspace);
      const base = `/t/real/w/${first.workspace}`;
      await page.goto(origin + base + '/memory');
      await expect(page.getByRole('link', { name: new RegExp(first.document) })).toContainText(first.content);
      await page.getByRole('link', { name: new RegExp(first.document) }).click();
      await expect(page.getByText(first.content, { exact: true }).first()).toBeVisible();
      await page.getByRole('link', { name: 'Open source conversation', exact: true }).click();
      await expect(page.getByRole('region', { name: 'Conversation messages' })).toContainText(first.source_text);
      await expect(page.getByRole('region', { name: 'Conversation memory' })).toContainText(first.content);
      await page.reload();
      await expect(page.getByRole('region', { name: 'Conversation messages' })).toContainText(first.source_text);
      await page.screenshot({ path: path.join(artifacts, 'browse-conversation-desktop.png'), fullPage: true });
      for (const path of [`/memory/summary/${second.document}`, `/conversations/${second.conversation}`, '/memory?user_id=' + second.owner]) {
        await page.goto(origin + base + path);
        await expect(page.getByRole('alert')).toBeVisible();
        assert(!(await page.content()).includes(second.content));
      }
      for (const path of [`/memory/documents/summary/${second.document}`, `/conversations/${second.conversation}/memory`, `/conversations/${second.conversation}/sources`]) {
        const response = await context.request.get(origin + `/manager/api/targets/real/api/admin/workspaces/${first.workspace}` + path);
        assert.equal(response.status(), 404);
      }
      // Each viewport gets its own page/cache lifecycle. Geometry checks do not
      // depend on WebKit retaining a font preload across repeated full reloads.
      for (const size of [{ width: 320, height: 640 }, { width: 390, height: 844 }, { width: 844, height: 390 }]) {
        const browseContext = await mobile.newContext({ viewport: size, isMobile: true, deviceScaleFactor: 1, hasTouch: true, storageState: await mobileContext.storageState() });
        try {
          const browsePage = await browseContext.newPage();
          for (const route of ['/memory', '/conversations']) {
            await browsePage.goto(origin + base + route, { waitUntil: 'domcontentloaded' });
            await expect(browsePage.getByRole('heading', { name: route === '/memory' ? 'Memory documents' : 'Conversations', exact: true })).toBeVisible();
            assert(await browsePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
            assert((await browsePage.getByLabel(route === '/memory' ? 'Filter documents' : 'Filter conversations').boundingBox()).height >= 44);
            await browsePage.screenshot({ path: path.join(artifacts, `browse-${route.slice(1)}-${size.width}.png`), fullPage: false });
          }
        } finally { await browseContext.close(); }
      }
      const anonymous = await browser.newContext();
      const response = await anonymous.request.get(origin + base + '/memory');
      assert(!(await response.text()).includes(first.content));
      await anonymous.close();
      report.checks.push('production SSR + PostgreSQL: document/source links, reload, foreign owner/document/conversation and related collections rejected, fresh browser isolated; WebKit 320/390/844px browse layouts');
    }
    const fresh = await browser.newContext();
    const freshPage = await fresh.newPage();
    const response = await freshPage.goto(origin + scopeURL);
    assert(!(await response.text()).includes('Runtime policy check'));
    await expect(freshPage.getByLabel('Workspace name')).toHaveCount(0);
    report.checks.push('fresh browser deep link cannot render another browser workspace data');
    await api('DELETE', `/api/admin/users/${owner}/api-keys/${issued.record.id}`);
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
