import { defineEnvVars } from '@sveltejs/kit/env';

export const variables = defineEnvVars({
  PUBLIC_MEMSTATION_DEFAULT_TARGET: {
    public: true,
    schema: (value) => value ?? ''
  }
});
