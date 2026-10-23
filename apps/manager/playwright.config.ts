import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { defineConfig, devices } from '@playwright/test';

const dirname = path.dirname(fileURLToPath(import.meta.url));
const managerPort = process.env.MEMSTATION_E2E_MANAGER_PORT || '4173';
const targetPort = process.env.MEMSTATION_E2E_TARGET_PORT || '18080';
const managerUrl = `http://127.0.0.1:${managerPort}`;
const targetUrl = `http://127.0.0.1:${targetPort}`;
const fixtureAdminToken = 'memstation-e2e-admin-token';

export default defineConfig({
  testDir: path.join(dirname, 'e2e'),
  testMatch: ['**/*.spec.ts'],
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  expect: {
    timeout: 10_000
  },
  reporter: 'list',
  use: {
    baseURL: managerUrl,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome']
      }
    },
    {
      name: 'mobile-webkit',
      grep: /mobile navigation|mobile filters|short mobile dialogs|mobile settings|memory search keeps|task queue changes|primary admin routes|system section navigation|memory detail prioritizes|memory historical filters|entity alias candidates|shared controls/,
      use: {
        ...devices['iPhone 13']
      }
    }
  ],
  webServer: [
    {
      command: `PORT=${targetPort} MANAGER_ORIGIN=${managerUrl} MEMSTATION_ADMIN_TOKEN=${fixtureAdminToken} MEMSTATION_GATEWAY_API_KEY=ms-e2e node ./e2e/fixture/target-server.mjs`,
      cwd: dirname,
      url: `${targetUrl}/__health`,
      timeout: 60_000,
      reuseExistingServer: false
    },
    {
      command: `PUBLIC_MEMSTATION_DEFAULT_TARGET=${targetUrl} pnpm run generate && pnpm exec vite dev --host 127.0.0.1 --port ${managerPort}`,
      cwd: dirname,
      url: managerUrl,
      timeout: 120_000,
      reuseExistingServer: false
    }
  ]
});
