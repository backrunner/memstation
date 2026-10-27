ALTER TABLE system_settings
  ADD COLUMN IF NOT EXISTS internal_default_provider TEXT;

CREATE TABLE provider_credentials_v2 (
  scope TEXT NOT NULL,
  provider TEXT NOT NULL,
  encrypted_api_key TEXT,
  base_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (scope, provider)
);

DROP TABLE provider_credentials;

ALTER TABLE provider_credentials_v2 RENAME TO provider_credentials;