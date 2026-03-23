import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { defineConfig } from 'vitest/config';

const rootDir = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  resolve: {
    alias: {
      $lib: path.resolve(rootDir, 'src/lib')
    }
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts']
  }
});
