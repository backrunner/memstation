const { createRequire } = require('node:module');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { createHash, randomBytes } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../..');
function required(name) { const value = process.env[name]; if (!value) throw new Error(`${name} is required`); return value; }
const targetURL = required('MEMSTATION_TEST_TARGET_URL');
const adminToken = required('MEMSTATION_TEST_ADMIN_TOKEN');
const redisURL = required('MEMSTATION_TEST_MANAGER_REDIS_URL');
if (process.env.MEMSTATION_TEST_MANAGER_REDIS_ALLOW_PAUSE !== '1') throw new Error('Use a disposable Redis instance and set MEMSTATION_TEST_MANAGER_REDIS_ALLOW_PAUSE=1.');
const firstPort = Number(process.env.MEMSTATION_TEST_MANAGER_PORT ?? 4274);
const secondPort = firstPort + 1;
const firstOrigin = `http://127.0.0.1:${firstPort}`;
const secondOrigin = `http://127.0.0.1:${secondPort}`;
const req = createRequire(path.join(root, 'apps/manager/package.json'));
const { chromium, expect } = req('@playwright/test');
const { createClient } = req('@redis/client');
const artifact = path.resolve(process.env.MEMSTATION_TEST_ARTIFACT_DIR ?? path.join(root, '.artifacts/manager-session-smoke'));
fs.mkdirSync(artifact, { recursive: true });
const secret = randomBytes(32).toString('hex');
const children = [];
const registry = JSON.stringify([{ id: 'real', label: 'Local Go service', baseUrl: targetURL }]);
const report = { goStore: 'provided test Go service', node: process.version, managerStore: 'real Redis with AES-256-GCM', productionBuild: true, checks: [] };
const log = fs.openSync(path.join(artifact, 'runtime-manager.log'), 'w');
async function start(port) {
  const probe = require('node:net').createServer();
  await new Promise((resolve, reject) => { probe.once('error', reject); probe.listen(port, '127.0.0.1', resolve); });
  await new Promise((resolve) => probe.close(resolve));
  const child = spawn(process.execPath, ['server.mjs'], { cwd: path.join(root, 'apps/manager'), stdio: ['ignore', log, log], env: { ...process.env, NODE_ENV: 'production', BODY_SIZE_LIMIT: '32M', HOST: '127.0.0.1', PORT: String(port), ORIGIN: `http://127.0.0.1:${port}`, MEMSTATION_MANAGER_TARGETS: registry, MEMSTATION_MANAGER_REDIS_URL: redisURL, MEMSTATION_MANAGER_SESSION_KEY: secret } });
  children.push(child);
  const origin = `http://127.0.0.1:${port}`;
  await expect.poll(async () => { try { return (await fetch(origin + '/login')).status; } catch { return 0; } }, { timeout: 20000 }).toBe(200);
  return child;
}
async function stop(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const done = once(child, 'exit'); child.kill('SIGTERM'); const timer = setTimeout(() => child.kill('SIGKILL'), 8000); await done; clearTimeout(timer); assert.notEqual(child.signalCode, 'SIGKILL', 'Manager must shut down gracefully');
}
(async () => {
  let browser, redis;
  try {
    const first = await start(firstPort);
    browser = await chromium.launch();
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(firstOrigin + '/login');
    await page.screenshot({ path: path.join(artifact, 'real-login.png') });
    await page.getByLabel('Admin token', { exact: true }).fill(adminToken);
    await page.getByRole('button', { name: 'Connect target', exact: true }).click();
    await expect(page).toHaveURL(/dashboard$/);
    await expect(page.getByRole('heading', { name: 'Dashboard', exact: true })).toBeVisible();
    const cookies = await context.cookies();
    const managerCookie = cookies.find(c => c.name === 'memstation_manager_session');
    assert(managerCookie?.httpOnly && managerCookie.secure && managerCookie.sameSite === 'Strict');
    assert(!cookies.some(c => c.name === 'memstation_admin_session'));
    report.checks.push('real Go login through production Manager; Secure/HttpOnly/SameSite cookie; no upstream browser cookie');
    await page.goto(firstOrigin + '/users');
    await expect(page.getByRole('heading', { name: 'Users', exact: true })).toBeVisible();
    await expect.poll(async () => (await page.request.get(firstOrigin + '/manager/api/targets/real/api/admin/users')).status()).toBe(200);
    const manager = firstOrigin;
    const credentials = await page.request.post(manager + '/manager/api/targets/real/api/admin/users/admin/api-keys', { headers: { origin: manager }, data: { name: 'manager-session-smoke' } });
    assert.equal(credentials.status(), 200);
    const issued = await credentials.json();
    const file = Buffer.alloc(600 * 1024, 65);
    const upload = await page.request.post(manager + '/manager/api/targets/real/v1/files', { headers: { origin: manager, authorization: `Bearer ${issued.api_key}` }, multipart: { file: { name: 'session-smoke.txt', mimeType: 'text/plain', buffer: file } } });
    assert.equal(upload.status(), 201);
    const blob = await upload.json();
    const download = await page.request.get(manager + '/manager/api/targets/real/api/admin/files/' + blob.blob_id + '?user_id=admin');
    assert.equal(download.status(), 200);
    assert((await download.body()).equals(file));
    assert(download.headers()['content-security-policy'].includes('sandbox'));
    assert.equal((await page.request.delete(manager + '/manager/api/targets/real/v1/files/' + blob.blob_id, { headers: { origin: manager, authorization: `Bearer ${issued.api_key}` } })).status(), 204);
    assert.equal((await page.request.delete(manager + '/manager/api/targets/real/api/admin/users/admin/api-keys/' + issued.record.id, { headers: { origin: manager } })).status(), 200);
    report.checks.push('real Go credential issue/revoke and 600 KiB multipart upload/download/delete through Manager');
    await page.screenshot({ path: path.join(artifact, 'real-users-desktop.png') });
    await page.getByRole('button', { name: 'Manage targets', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Target connections' })).toBeVisible();
    await page.screenshot({ path: path.join(artifact, 'connection-desktop.png') });
    for (const viewport of [{ width: 390, height: 844 }, { width: 320, height: 568 }, { width: 844, height: 390 }]) {
      await page.setViewportSize(viewport);
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.getByRole('button', { name: 'Save target', exact: true }).scrollIntoViewIfNeeded();
      await expect(page.getByRole('button', { name: 'Save target', exact: true })).toBeInViewport();
      await page.screenshot({ path: path.join(artifact, `connection-${viewport.width}x${viewport.height}.png`) });
    }
    await page.keyboard.press('Escape');
    report.checks.push('desktop, 390px, 320px and short landscape connection dialog');
    redis = createClient({ url: redisURL }); await redis.connect();
    const keys = await redis.keys(`memstation:manager:v1:${createHash('sha256').update(managerCookie.value).digest('hex')}:target:real:*`);
    assert(keys.length > 0);
    for (const key of keys) { const value = await redis.get(key); assert(value && !value.includes('memstation_admin_session') && !value.includes(adminToken)); }
    report.checks.push('Redis stores encrypted upstream sessions without bootstrap token');
    await stop(first);
    await start(firstPort);
    await page.reload();
    await expect.poll(async () => (await page.request.get(firstOrigin + '/manager/api/targets/real/api/admin/users')).status()).toBe(200);
    report.checks.push('Manager restart preserves authenticated requests');
    await start(secondPort);
    await page.goto(secondOrigin + '/users');
    await expect.poll(async () => (await page.request.get(secondOrigin + '/manager/api/targets/real/api/admin/users')).status()).toBe(200);
    report.checks.push('second Manager process shares the same encrypted browser session');
    const fresh = await browser.newContext();
    assert.equal((await fresh.request.get(secondOrigin + '/manager/api/targets/real/api/admin/users')).status(), 401);
    await fresh.close();
    report.checks.push('fresh browser cannot reuse another browser connection');
    await redis.sendCommand(['CLIENT', 'PAUSE', '6500', 'ALL']);
    assert.equal((await page.request.get(secondOrigin + '/manager/api/targets/real/api/admin/users')).status(), 503);
    await expect.poll(async () => (await page.request.get(secondOrigin + '/manager/api/targets/real/api/admin/users')).status(), { timeout: 15000 }).toBe(200);
    report.checks.push('Redis pause fails closed within command timeout and recovers without Manager restart');
    await redis.del(keys);
    assert.equal((await page.request.get(secondOrigin + '/manager/api/targets/real/api/admin/users')).status(), 401);
    await page.reload();
    await expect(page.getByRole('button', { name: 'Reconnect', exact: true })).toBeVisible();
    report.checks.push('missing/expired Redis binding denies access and shows reconnect state');
    assert.deepEqual(errors, []);
    report.ok = true;
  } catch (error) { report.ok = false; report.error = String(error); throw error; }
  finally {
    if (redis?.isOpen) redis.destroy();
    if (browser) await browser.close();
    for (const child of children) await stop(child);
    fs.closeSync(log);
    fs.writeFileSync(path.join(artifact, 'runtime-validation.json'), JSON.stringify(report, null, 2) + '\n');
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
