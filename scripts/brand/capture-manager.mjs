import { spawn, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

// Always use synthetic repository fixtures, never a user's running service.
const root = new URL('../../', import.meta.url);
const manager = new URL('apps/manager/', root);
const require = createRequire(new URL('package.json', manager));
const { chromium, expect } = require('@playwright/test');
const managerURL = 'http://127.0.0.1:18471';
const targetURL = 'http://127.0.0.1:18472';
const adminToken = 'memstation-e2e-admin-token';
const targets = [{ id: 'fixture-target', label: 'Local demo', baseUrl: targetURL }];
const env = {
  ...process.env,
  MEMSTATION_MANAGER_TARGETS: JSON.stringify(targets),
  MEMSTATION_MANAGER_REDIS_URL: '',
  MEMSTATION_MANAGER_SESSION_KEY: '',
  MEMSTATION_MANAGER_DEV_PROXY_TARGET: targetURL
};
const children = [];
let browser;

async function waitUntilReady(url, child) {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`Screenshot service exited: ${url}`);
    try {
      if ((await fetch(url, { signal: AbortSignal.timeout(1000) })).ok) return;
    } catch { /* Startup can take a few seconds. */ }
    await delay(250);
  }
  throw new Error(`Screenshot service did not start: ${url}`);
}

async function assertPortAvailable(url) {
  const { createServer } = await import('node:net');
  const server = createServer();
  server.listen(Number(new URL(url).port), '127.0.0.1');
  await once(server, 'listening');
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}

async function capture(page, route, heading, filename) {
  await page.goto(`${managerURL}${route}`);
  await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible();
  await expect(page.locator('.error-state')).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: fileURLToPath(new URL(`docs/assets/${filename}`, root)), animations: 'disabled' });
}

try {
  await assertPortAvailable(managerURL);
  await assertPortAvailable(targetURL);
  await mkdir(new URL('docs/assets/', root), { recursive: true });
  execFileSync(process.execPath, [fileURLToPath(new URL('node_modules/@sveltejs/kit/svelte-kit.js', manager)), 'sync'], { cwd: manager, env, stdio: 'inherit' });
  const target = spawn(process.execPath, [fileURLToPath(new URL('e2e/fixture/target-server.mjs', manager))], {
    cwd: manager, env: { ...env, PORT: '18472', MANAGER_ORIGIN: managerURL, MEMSTATION_ADMIN_TOKEN: adminToken }, stdio: 'inherit'
  });
  children.push(target);
  await waitUntilReady(`${targetURL}/__health`, target);
  const vite = spawn(process.execPath, [fileURLToPath(new URL('node_modules/vite/bin/vite.js', manager)), '--host', '127.0.0.1', '--port', '18471', '--strictPort'], { cwd: manager, env, stdio: 'inherit' });
  children.push(vite);
  await waitUntilReady(managerURL, vite);
  browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1, colorScheme: 'light', reducedMotion: 'reduce', locale: 'en-US', timezoneId: 'UTC' });
  await page.goto(`${managerURL}/login`);
  await page.getByLabel('Connection URI or base URL').fill(targetURL);
  await page.getByLabel('Label', { exact: true }).fill('Local demo');
  await page.getByPlaceholder('MEMSTATION_ADMIN_TOKEN').fill(adminToken);
  await page.getByRole('button', { name: 'Connect target', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.locator('canvas').first()).toBeVisible();
  await capture(page, '/dashboard', 'Dashboard', 'manager-preview.png');
  await capture(page, '/t/fixture-target/w/personal/memory', 'Memory documents', 'manager-memory.png');
  await page.setViewportSize({ width: 390, height: 844 });
  await capture(page, '/t/fixture-target/w/personal/memory', 'Memory documents', 'manager-mobile.png');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  console.log('Captured dashboard, workspace memory and mobile screenshots using synthetic fixtures.');
} finally {
  await browser?.close();
  for (const child of children.reverse()) {
    if (child.exitCode !== null) continue;
    const exited = once(child, 'exit');
    child.kill('SIGTERM');
    await exited;
  }
}
