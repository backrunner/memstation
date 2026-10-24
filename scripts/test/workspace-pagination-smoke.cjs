// Run ONLY against a disposable, migrated Go service with workspace-page fixtures.
// Exercises paging, scope navigation, review links and conditional key deletion.
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
const artifacts = path.resolve(process.env.MEMSTATION_TEST_ARTIFACT_DIR ?? path.join(root, '.artifacts/workspace-pagination-smoke'));
const runId = `workspace-pagination-run-${randomUUID()}`;
fs.mkdirSync(artifacts, { recursive: true });
const report = { node: process.version, productionBuild: true, runId, checks: [] };
const log = fs.openSync(path.join(artifacts, 'manager-runtime.log'), 'w');

(async () => {
  let child, browser, mobile, page;
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
    page = await context.newPage();
    await page.goto(origin + '/login');
    await page.getByLabel('Connection URI or base URL').fill(target);
    await page.getByLabel('Label', { exact: true }).fill('Local Go service');
    await page.getByPlaceholder('MEMSTATION_ADMIN_TOKEN').fill(token);
    const connected = page.waitForResponse(response => response.url().includes('/manager/session/') && response.request().method() === 'POST');
    await page.getByRole('button', { name: 'Connect target', exact: true }).click();
    const response = await connected;
    assert(response.ok(), `Manager connect: ${response.status()}`);
    await expect(page).toHaveURL(/\/dashboard$/, { timeout: 15000 });
    async function api(method, resource, data) {
      const response = await context.request.fetch(origin + '/manager/api/targets/real' + resource, { method, headers: { origin }, data });
      assert(response.ok(), `${method} ${resource}: ${response.status()}`);
      return response.json();
    }
    const [first, second] = JSON.parse(fs.readFileSync(required('MEMSTATION_TEST_PAGINATION_FIXTURES'), 'utf8'));
    assert(first.owner.startsWith('workspace-page-') && first.workspace !== second.workspace);
    report.fixtureOwners = [first.owner, second.owner];
    const base = `/t/real/w/${first.workspace}`;
    for (const [section, resource, label, next] of [
      ['conversations', 'conversations', 'Filter conversations', 'Next conversations'],
      ['keys', 'memory/keys', 'Filter key memories', 'Next key memories']
    ]) {
      await page.goto(origin + base + `/${section}?q=page-`);
      await expect(page.locator('.record-row')).toHaveCount(30);
      const one = await page.locator('.record-row').allTextContents();
      await page.getByRole('link', {name:next,exact:true}).click();
      await expect(page).toHaveURL(/after=/);
      await expect(page.locator('.record-row')).toHaveCount(30);
      assert(!(await page.locator('.record-row').allTextContents()).some(item=>one.includes(item)));
      await page.getByLabel(label).fill('old-archive');
      await page.getByRole('button',{name:'Apply',exact:true}).click();
      await expect(page).not.toHaveURL(/after=/);
      await expect(page.locator('.record-row')).toHaveCount(1);
      await expect(page.locator('.record-row')).toContainText(first.owner+'-old-archive');
      await page.reload();
      await expect(page.getByLabel(label)).toHaveValue('old-archive');
      await page.screenshot({path:path.join(artifacts,`runtime-${section}-desktop.png`),fullPage:true});
      const firstPage = await api('GET', `/api/admin/workspaces/${first.workspace}/${resource}/page?limit=2`);
      assert(firstPage.next_cursor && firstPage.items.length===2);
      const bad = await context.request.get(origin+`/manager/api/targets/real/api/admin/workspaces/${second.workspace}/${resource}/page?limit=2&after=${encodeURIComponent(firstPage.next_cursor)}`);
      assert.equal(bad.status(),400);
      await page.goto(origin + base + `/${section}?q=old-archive&after=broken`);
      await expect(page.getByRole('alert')).toContainText('Invalid filters');
      await page.getByRole('link',{name:'First page',exact:true}).click();
      await expect(page.locator('.record-row')).toHaveCount(1);
      await page.locator('.record-row').click();
      const review = page.getByRole('link',{name:section==='keys'?'Review history and metadata':'Processing and rebuild controls',exact:true});
      await expect(review).toHaveAttribute('href', /manager_target=real/);
      await review.click();
      await expect(page).toHaveURL(/manager_target=real/);
      await expect(page.getByRole('heading',{name:first.owner+'-old-archive',exact:true})).toBeVisible();
      report.checks.push(`${section}: production SSR 30-row pages, no duplicate rows, old records beyond 100, recoverable cursor failure, cross-owner rejection, pinned advanced review`);
    }
    await page.goto(origin+`/t/real/open/memory/keys?user_id=${encodeURIComponent(first.owner)}`);
    await expect(page).toHaveURL(origin+base+'/keys');
    await page.goto(origin+'/t/real/open/conversations');
    await expect(page).toHaveURL(/workspaces\?open=/);
    await page.getByLabel('Search workspaces').fill('Alpha workspace');
    await page.getByRole('button',{name:'Search',exact:true}).click();
    await page.getByRole('link',{name:new RegExp('Alpha workspace Owner: '+first.owner)}).click();
    await expect(page).toHaveURL(origin+base+'/conversations');
    const foreign = await context.request.get(origin+`/manager/api/targets/real/api/admin/workspaces/${first.workspace}/conversations/page?user_id=${second.owner}`);
    assert.equal(foreign.status(),404);
    const unsupported = await context.request.get(origin+'/manager/api/targets/real/api/admin/workspaces/group/conversations/page');
    assert.equal(unsupported.status(),404);
    const key = await api('GET',`/api/admin/workspaces/${first.workspace}/memory/keys/${first.owner}-old-archive`);
    await api('DELETE',`/api/admin/workspaces/${first.workspace}/memory/keys/${key.id}`,{expected_updated_at:key.updated_at});
    const deleted = await api('GET',`/api/admin/workspaces/${first.workspace}/memory/keys/page?q=old-archive`);
    assert.equal(deleted.items.length,0);
    report.checks.push('owner-aware navigation, multiple workspaces require choice, scope/unsupported workspace rejected, conditional deleted key stays absent');
    mobile=await webkit.launch();
    for (const width of [320,390]) {
      const mobileContext=await mobile.newContext({viewport:{width,height:844},isMobile:true,hasTouch:true,deviceScaleFactor:1,storageState:await context.storageState()});
      try {
        const mobilePage=await mobileContext.newPage();
        for (const section of ['conversations','keys']) {
          await mobilePage.goto(origin+base+`/${section}?q=page-000`);
          await expect(mobilePage.locator('.record-row')).toHaveCount(1);
          assert(await mobilePage.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
          assert((await mobilePage.getByRole('button',{name:'Apply',exact:true}).boundingBox()).height>=44);
          await mobilePage.screenshot({path:path.join(artifacts,`runtime-${section}-${width}.png`),fullPage:true});
        }
      } finally { await mobileContext.close(); }
    }
    const anonymous=await browser.newContext();
    try { const response=await anonymous.request.get(origin+base+'/conversations'); assert(!(await response.text()).includes(first.owner+'-page-')); } finally { await anonymous.close(); }
    report.checks.push('WebKit 320/390: no horizontal overflow, 44px controls; anonymous SSR contains no private records');
    fs.writeFileSync(path.join(artifacts,'runtime-validation.json'),JSON.stringify(report,null,2));
  } catch (error) {
    if (page && !page.isClosed()) { await page.screenshot({ path: path.join(artifacts, 'runtime-failure.png'), fullPage: true }); console.error('Visible errors:', await page.getByRole('alert').allTextContents()); }
    throw error;
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
