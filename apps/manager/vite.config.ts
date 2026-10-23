import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';
import type { ClientRequest } from 'node:http';
import type { ProxyOptions } from 'vite';
import { defineConfig } from 'vitest/config';

const devProxyTarget = process.env.MEMSTATION_MANAGER_DEV_PROXY_TARGET?.trim();

function managerDevProxy() {
  if (!devProxyTarget) {
    return undefined;
  }

  return Object.fromEntries(
    ['/api', '/health', '/graphql', '/v1'].map((path) => [
      path,
      {
        target: devProxyTarget,
        changeOrigin: true,
        secure: false,
        configure(proxy: Parameters<NonNullable<ProxyOptions['configure']>>[0]) {
          proxy.on('proxyReq', (proxyReq: ClientRequest) => {
            proxyReq.removeHeader('origin');
          });
        }
      }
    ])
  );
}

export default defineConfig({
  plugins: [sveltekit({ adapter: adapter() })],
  server: {
    host: '0.0.0.0',
    port: 3000,
    proxy: managerDevProxy()
  },
  test: {
    include: ['src/**/*.test.ts', 'src/**/*.spec.ts'],
    environment: 'node',
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json-summary'],
      thresholds: {
        lines: 70,
        functions: 70,
        branches: 60,
        statements: 70
      }
    }
  }
});
