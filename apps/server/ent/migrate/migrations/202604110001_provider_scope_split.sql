ALTER TABLE system_settings
  ADD COLUMN IF NOT EXISTS internal_default_provider TEXT;

UPDATE system_settings
SET default_provider = 'openai_compatible'
WHERE LOWER(COALESCE(default_provider, '')) = 'openai';

UPDATE system_settings
SET internal_default_provider = CASE
  WHEN LOWER(COALESCE(default_provider, '')) = 'openai' THEN 'openai_compatible'
  ELSE default_provider
END
WHERE COALESCE(BTRIM(internal_default_provider), '') = '';

UPDATE system_settings
SET internal_default_provider = 'openai_compatible'
WHERE LOWER(COALESCE(internal_default_provider, '')) = 'openai';

CREATE TABLE provider_credentials_v2 (
  scope TEXT NOT NULL,
  provider TEXT NOT NULL,
  encrypted_api_key TEXT,
  base_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (scope, provider)
);

INSERT INTO provider_credentials_v2 (
  scope,
  provider,
  encrypted_api_key,
  base_url,
  created_at,
  updated_at
)
SELECT
  'gateway',
  CASE
    WHEN LOWER(provider) = 'openai' THEN 'openai_compatible'
    ELSE provider
  END,
  encrypted_api_key,
  base_url,
  created_at,
  updated_at
FROM provider_credentials;

INSERT INTO provider_credentials_v2 (
  scope,
  provider,
  encrypted_api_key,
  base_url,
  created_at,
  updated_at
)
SELECT
  'internal',
  CASE
    WHEN LOWER(provider) = 'openai' THEN 'openai_compatible'
    ELSE provider
  END,
  encrypted_api_key,
  base_url,
  created_at,
  updated_at
FROM provider_credentials;

INSERT INTO provider_credentials_v2 (
  scope,
  provider,
  encrypted_api_key,
  base_url,
  created_at,
  updated_at
)
SELECT
  'embedding',
  'openai_compatible',
  previous_embedding.embedding_api_key_encrypted,
  previous_embedding.embedding_base_url,
  previous_embedding.created_at,
  previous_embedding.updated_at
FROM (
  SELECT
    embedding_api_key_encrypted,
    embedding_base_url,
    created_at,
    updated_at
  FROM user_settings
  WHERE COALESCE(BTRIM(embedding_base_url), '') <> ''
     OR COALESCE(BTRIM(embedding_api_key_encrypted), '') <> ''
  ORDER BY updated_at DESC, created_at DESC
  LIMIT 1
) AS previous_embedding
ON CONFLICT (scope, provider) DO NOTHING;

DROP TABLE provider_credentials;

ALTER TABLE provider_credentials_v2 RENAME TO provider_credentials;
