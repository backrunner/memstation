// Run ONLY against a disposable, migrated Go service with workspace-edit fixture
// owners. Reads pre-seeded pagination and historical correction fixtures.
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
const artifacts = path.resolve(process.env.MEMSTATION_TEST_ARTIFACT_DIR ?? path.join(root, '.artifacts/workspace-search-smoke'));
const runId = `workspace-search-run-${randomUUID()}`;
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
    const [first, second] = JSON.parse(fs.readFileSync(required('MEMSTATION_TEST_WORKSPACE_EDIT_FIXTURES'), 'utf8'));
    assert(first?.workspace && second?.workspace && first.workspace !== second.workspace);
    assert(first.owner.startsWith('workspace-edit-'), 'Use the disposable workspace-edit fixture owner');
    report.fixtureOwners = [first.owner, second.owner];
    const base = `/t/real/w/${first.workspace}`;
    const resource = `/api/admin/workspaces/${first.workspace}/memory`;
    await page.goto(origin + base + '/memory?q=paginated&kind=summary');
    await expect(page.locator('.record-row')).toHaveCount(30);
    const firstPage = await page.locator('.record-row').allTextContents();
    await page.getByRole('link', { name: 'Next documents', exact: true }).click();
    await expect(page).toHaveURL(/after=/);
    await expect(page.locator('.record-row').first()).not.toHaveText(firstPage[0]);
    await expect(page.locator('.record-row')).toHaveCount(30);
    assert(!(await page.locator('.record-row').allTextContents()).some(item => firstPage.includes(item)));
    await page.getByLabel('Filter documents').fill('unique old archive');
    await page.getByRole('button', { name: 'Apply', exact: true }).click();
    await expect(page.getByRole('link', { name: /Alpha unique old archive evidence/ })).toBeVisible();
    assert(!new URL(page.url()).searchParams.has('after'));
    report.checks.push('production SSR filters records beyond 100, bounded pages, no repeated rows, new filter clears cursor');
    const pageOne = await api('GET', resource + '/documents/page?kind=summary&limit=2');
    assert(pageOne.next_cursor && pageOne.items.length === 2);
    const mismatched = await context.request.get(origin + '/manager/api/targets/real' + `/api/admin/workspaces/${second.workspace}/memory/documents/page?kind=summary&limit=2&after=${encodeURIComponent(pageOne.next_cursor)}`);
    assert.equal(mismatched.status(), 400);
    const bad = await context.request.post(origin + '/manager/api/targets/real' + resource + '/search', {headers: {origin}, data: {query:'archival',top_n:10,user_id:second.owner}});
    assert.equal(bad.status(),400);
    const history = await api('POST', resource + '/search', {query:'archival',top_n:10,known_at:first.known_at});
    assert.equal(history.memories.length,1);
    assert.equal(history.memories[0].content,'Alpha archival preference: green tea');
    assert.equal(history.memories[0].metadata.historical_snapshot,true);
    const live = await api('POST', resource + '/search', {query:'archival',top_n:10,memory_types:['fact']});
    assert.equal(live.memories[0].content,'Alpha archival preference: black coffee');
    const searchURL = origin + base + '/memory/search?' + new URLSearchParams({q:'archival',kind:'fact',known_at:first.known_at});
    await page.goto(searchURL);
    await expect(page.getByText('Alpha archival preference: green tea', {exact:true})).toBeVisible();
    await expect(page.getByRole('button', {name:'Edit memory'})).toHaveCount(0);
    await page.reload();
    await expect(page.getByLabel('Known by the system at')).toHaveValue(first.known_at);
    await page.screenshot({path:path.join(artifacts,'history-desktop.png'),fullPage:true});
    await page.getByRole('link', {name:'Open current record'}).click();
    await expect(page.getByText('Alpha archival preference: black coffee',{exact:true})).toBeVisible();
    report.checks.push('real fact-history snapshot survives later correction; SSR read-only history reload, explicit current-record link');
    await page.goto(searchURL + '&generation_id=' + encodeURIComponent(first.generation));
    await page.getByRole('button', {name:'Switch workspace: Alpha workspace'}).click();
    await page.getByLabel('Find a workspace').fill('Beta workspace');
    await page.getByRole('dialog').getByRole('button', {name:'Search',exact:true}).click();
    await page.getByRole('dialog').getByRole('link', {name:/Beta workspace/}).click();
    await expect(page).toHaveURL(new RegExp(`/w/${second.workspace}/memory/search`));
    assert(new URL(page.url()).pathname.includes(second.workspace));
    assert(!new URL(page.url()).searchParams.has('generation_id'));
    assert.equal(new URL(page.url()).searchParams.get('known_at'),first.known_at);
    await expect(page.getByText('Alpha archival preference: green tea',{exact:true})).toHaveCount(0);
    await page.goto(origin + base + '/memory/search?q=archival&as_of=invalid');
    await expect(page.getByRole('alert')).toContainText('Invalid filters');
    await page.goto(origin + base + '/memory?after=invalid');
    await expect(page.getByRole('alert')).toContainText('Invalid filters');
    await page.getByRole('link', {name:'First page',exact:true}).click();
    await expect(page.locator('.record-row')).toHaveCount(30);
    report.checks.push('scope picker retains search/history, clears generation, no foreign results; malformed timestamp/cursor yields recoverable error');
    mobile = await webkit.launch();
    for (const size of [{width:320,height:640},{width:390,height:844},{width:844,height:390}]) {
      const mobileContext = await mobile.newContext({viewport:size,isMobile:true,hasTouch:true,deviceScaleFactor:1,storageState:await context.storageState()});
      try {
        const mobilePage = await mobileContext.newPage();
        await mobilePage.goto(searchURL);
        await expect(mobilePage.getByText('Alpha archival preference: green tea',{exact:true})).toBeVisible();
        assert(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth+1));
        assert((await mobilePage.getByLabel('Search memory',{exact:true}).boundingBox()).height>=44);
        assert((await mobilePage.getByLabel('Search memory',{exact:true}).evaluate(el=>parseFloat(getComputedStyle(el).fontSize)))>=16);
        await mobilePage.screenshot({path:path.join(artifacts,`history-${size.width}.png`),fullPage:true});
        await mobilePage.getByRole('button',{name:'Switch workspace: Alpha workspace'}).click();
        await mobilePage.getByLabel('Find a workspace').fill('Beta workspace');
        await mobilePage.getByRole('dialog').getByRole('button',{name:'Search',exact:true}).click();
        await expect(mobilePage.getByRole('dialog').getByRole('link',{name:/Beta workspace/})).toBeVisible();
        assert(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth+1));
        await mobilePage.screenshot({path:path.join(artifacts,`picker-${size.width}.png`),fullPage:false});
        await mobilePage.getByRole('dialog').getByRole('button',{name:'Close',exact:true}).click();
        await expect(mobilePage.getByRole('dialog')).toHaveCount(0);
      } finally { await mobileContext.close(); }
    }
    report.checks.push('WebKit 320/390/844 history and picker, no page overflow, 44px controls, 16px inputs, scrollable short dialog');
    const anonymous = await browser.newContext();
    try {
      const response = await anonymous.request.get(searchURL);
      assert(!(await response.text()).includes('Alpha archival preference: green tea'));
    } finally { await anonymous.close(); }
    report.checks.push('anonymous SSR does not contain private search results');
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
