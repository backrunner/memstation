import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';

const require = createRequire(new URL('../../../../apps/manager/package.json', import.meta.url));
const { chromium, webkit } = require('@playwright/test');
const base = process.env.MEMSTATION_PLAN_PREVIEW_URL || 'http://127.0.0.1:4188';
const artifacts = new URL('../preview/', import.meta.url);
await mkdir(artifacts, { recursive: true });
const checks = [];
const errors = [];
const selectedBrowsers = (process.env.MEMSTATION_PLAN_BROWSERS || 'chromium,webkit').split(',');
assert(selectedBrowsers.every(name => ['chromium', 'webkit'].includes(name)), 'Unknown browser');

for (const name of selectedBrowsers) {
  const engine = { chromium, webkit }[name];
  const browser = await engine.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.on('pageerror', error => errors.push(`${name}: ${error.message}`));
    for (const width of [320, 390, 768, 1280, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      for (const view of ['overview', 'memory', 'subjects', 'agents', 'access', 'runs', 'conversations', 'workspaces']) {
        await page.goto(`${base}/#workspace=project&view=${view}`);
        await page.locator('h1').waitFor();
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1);
        assert.equal(overflow, false, `${name} ${width} ${view} horizontal overflow`);
      }
      checks.push({ browser: name, check: 'layout', width, views: 8, passed: true });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(`${base}/#workspace=project&view=memory`);
    await page.getByLabel('搜索记忆', { exact: true }).fill('Go');
    await page.getByRole('button', { name: '搜索', exact: true }).click();
    assert.equal(await page.locator('.ms-record').count(), 1);
    await page.locator('.ms-record-title').click();
    await page.getByRole('heading', { name: '来源证据' }).waitFor();
    await page.getByRole('button', { name: '编辑', exact: true }).click();
    await page.getByLabel('正文', { exact: true }).fill('Go 服务统一负责业务授权与编排。');
    await page.getByLabel('修改理由').fill('示例：明确授权职责');
    await page.getByRole('button', { name: '保存修改' }).click();
    assert.match(await page.locator('#status').textContent(), /版本 4/);
    await page.getByRole('link', { name: '查看原始消息 →' }).click();
    await page.locator('.ms-message.is-selected').waitFor();
    checks.push({ browser: name, check: 'search-edit-evidence', passed: true });

    await page.selectOption('#workspace-picker', 'group');
    await page.waitForFunction(() => document.querySelector('#breadcrumb').textContent.includes('摄影交流群'));
    assert.doesNotMatch(await page.locator('#content').textContent(), /Go 服务/);
    await page.goto(`${base}/#workspace=group&view=subjects&selected=${encodeURIComponent('林青')}`);
    await page.getByRole('heading', { name: '印象与推断' }).waitFor();
    assert.match(await page.locator('.ms-inspector').textContent(), /本人自述/);
    assert.match(await page.locator('.ms-inspector').textContent(), /模型推断/);
    checks.push({ browser: name, check: 'workspace-switch-subject-profile', passed: true });

    await page.goto(`${base}/#workspace=project&view=access`);
    await page.locator('[data-grant="reviewer"]').click();
    await page.getByLabel('调用网关', { exact: true }).check();
    await page.getByRole('button', { name: '保存访问范围' }).click();
    assert.match(await page.getByRole('row').filter({ hasText: '审查 Agent' }).textContent(), /调用网关/);
    checks.push({ browser: name, check: 'agent-grant-edit', passed: true });

    await page.setViewportSize({ width: 390, height: 500 });
    await page.goto(`${base}/#workspace=group&view=memory&selected=g1`);
    await page.getByRole('button', { name: '编辑', exact: true }).click();
    await page.getByLabel('修改理由').fill('短屏示例');
    await page.getByRole('button', { name: '保存修改' }).scrollIntoViewIfNeeded();
    await page.getByRole('button', { name: '保存修改' }).click();
    await page.getByRole('button', { name: '打开导航' }).click();
    await page.selectOption('#mobile-workspace-picker', 'personal');
    await page.waitForFunction(() => document.querySelector('#breadcrumb').textContent.includes('个人事务'));
    assert.equal(await page.locator('#nav-dialog').isVisible(), false);
    await page.getByRole('button', { name: '打开导航' }).click();
    await page.keyboard.press('Escape');
    assert.equal(await page.getByRole('button', { name: '打开导航' }).evaluate(el => el === document.activeElement), true);
    checks.push({ browser: name, check: 'short-dialog-mobile-navigation-focus', passed: true });

    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(`${base}/#workspace=project&view=memory&selected=p1`);
    await page.reload();
    await page.locator('.ms-inspector').waitFor();
    await page.mouse.move(1420, 980);
    if (name === 'chromium') await page.screenshot({ path: new URL('desktop.png', artifacts).pathname, fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`${base}/#workspace=group&view=subjects&selected=${encodeURIComponent('林青')}`);
    await page.locator('.ms-inspector').waitFor();
    if (name === 'webkit') await page.screenshot({ path: new URL('mobile.png', artifacts).pathname, fullPage: true });
  } finally { await browser.close(); }
}
assert.deepEqual(errors, [], 'Browser runtime errors');
const report = { generated_at: new Date().toISOString(), scope: 'synthetic prototype only', checks, browser_errors: errors };
await writeFile(new URL('checks.json', artifacts), JSON.stringify(report, null, 2) + '\n');
console.log(JSON.stringify({ passed: checks.length, layout_combinations: selectedBrowsers.length * 40, browser_errors: errors.length }));
