import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { defineConfig, devices } from '@playwright/test';

const dirname = path.dirname(fileURLToPath(import.meta.url));
const managerUrl = 'http://127.0.0.1:4173';
const targetUrl = 'http://127.0.0.1:18080';
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
      grep: /mobile navigation|mobile filters|short mobile dialogs|mobile settings|memory search keeps|task queue changes|primary admin routes|system section navigation|memory detail prioritizes/,
      use: {
        ...devices['iPhone 13']
      }
    }
  ],
  webServer: [
    {
      command: `PORT=18080 MANAGER_ORIGIN=http://127.0.0.1:4173 MEMSTATION_ADMIN_TOKEN=${fixtureAdminToken} MEMSTATION_GATEWAY_API_KEY=ms-e2e node ./e2e/fixture/target-server.mjs`,
      cwd: dirname,
      url: `${targetUrl}/__health`,
      timeout: 60_000,
      reuseExistingServer: false
    },
    {
      command: 'PUBLIC_MEMSTATION_DEFAULT_TARGET=http://127.0.0.1:18080 pnpm run generate && pnpm exec vite dev --host 127.0.0.1 --port 4173',
      cwd: dirname,
      url: managerUrl,
      timeout: 120_000,
      reuseExistingServer: false
    }
  ]
});
