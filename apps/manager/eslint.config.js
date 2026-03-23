import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import tseslint from 'typescript-eslint';

import svelteConfig from './svelte.config.js';

export default tseslint.config(
  {
    ignores: ['.svelte-kit/**', 'build/**', 'dist/**', 'coverage/**', 'node_modules/**', 'playwright-report/**', 'test-results/**']
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...svelte.configs.recommended,
  {
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node
      }
    }
  },
  {
    files: ['**/*.svelte', '**/*.svelte.js', '**/*.svelte.ts'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
        extraFileExtensions: ['.svelte'],
        svelteConfig
      }
    },
    rules: {
      'no-useless-assignment': 'off',
      'svelte/no-navigation-without-resolve': 'off',
      'svelte/no-immutable-reactive-statements': 'off',
      'svelte/prefer-svelte-reactivity': 'off',
      'svelte/require-each-key': 'off'
    }
  },
  {
    files: ['src/lib/components/ChatMessageCard.svelte'],
    rules: {
      'svelte/no-at-html-tags': 'off'
    }
  },
  {
    files: ['src/lib/components/EChart.svelte'],
    rules: {
      'svelte/infinite-reactive-loop': 'off'
    }
  },
  {
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_'
        }
      ]
    }
  }
);
