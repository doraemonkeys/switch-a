-- Frozen pre-CredentialSession schema. Every credential is synthetic and
-- intentionally carries the "fixture" marker; no value came from a real login.

BEGIN IMMEDIATE;

DROP TABLE IF EXISTS provider_auth_states;
DROP TABLE IF EXISTS provider_credentials;
DROP TABLE IF EXISTS provider_api_types;
DROP TABLE IF EXISTS providers;
DROP TABLE IF EXISTS groups;

CREATE TABLE groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    strategy TEXT DEFAULT 'priority',
    priority INTEGER DEFAULT 0,
    weight INTEGER DEFAULT 1,
    enabled NUMERIC DEFAULT 1,
    created_at DATETIME,
    updated_at DATETIME
);

CREATE TABLE providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    api_key TEXT NOT NULL,
    auth_mode TEXT DEFAULT 'auto',
    credential_type TEXT DEFAULT 'api_key',
    usage_limit_policy TEXT DEFAULT '',
    group_id TEXT,
    weight INTEGER DEFAULT 1,
    priority INTEGER DEFAULT 0,
    concurrency INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 0,
    backoff_initial_delay INTEGER,
    backoff_max_delay INTEGER,
    backoff_multiplier REAL,
    backoff_jitter NUMERIC,
    vendor TEXT,
    failover_scope TEXT DEFAULT 'any',
    accept_failover TEXT DEFAULT 'any',
    enabled NUMERIC DEFAULT 1,
    created_at DATETIME,
    updated_at DATETIME,
    CONSTRAINT fk_groups_providers FOREIGN KEY (group_id) REFERENCES groups(id)
);
CREATE INDEX idx_providers_group_id ON providers(group_id);
CREATE INDEX idx_providers_vendor ON providers(vendor);
CREATE INDEX idx_providers_enabled ON providers(enabled);

CREATE TABLE provider_api_types (
    provider_id TEXT NOT NULL,
    api_type TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    api_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (provider_id, api_type),
    CONSTRAINT fk_providers_api_types FOREIGN KEY (provider_id) REFERENCES providers(id)
);
CREATE INDEX idx_provider_api_types_api_type ON provider_api_types(api_type);

CREATE TABLE provider_credentials (
    provider_id TEXT PRIMARY KEY,
    secret_data TEXT DEFAULT '',
    binding_account_id TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME,
    updated_at DATETIME,
    CONSTRAINT fk_providers_credential FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX idx_provider_credentials_binding_account_id
    ON provider_credentials(binding_account_id);

CREATE TABLE provider_auth_states (
    provider_id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'not_connected',
    status_reason TEXT DEFAULT '',
    last_error TEXT DEFAULT '',
    last_transition_at DATETIME,
    email TEXT DEFAULT '',
    account_id TEXT DEFAULT '',
    plan_type TEXT DEFAULT '',
    expires_at DATETIME,
    last_refresh_at DATETIME,
    usage_snapshot TEXT,
    refresh_fail_count INTEGER NOT NULL DEFAULT 0,
    last_refresh_failure_at DATETIME,
    created_at DATETIME,
    updated_at DATETIME,
    CONSTRAINT fk_providers_auth_state FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
CREATE INDEX idx_provider_auth_states_status ON provider_auth_states(status);
CREATE INDEX idx_provider_auth_states_account_id ON provider_auth_states(account_id);

INSERT INTO providers (
    id, name, api_key, auth_mode, credential_type, usage_limit_policy,
    vendor, failover_scope, accept_failover, enabled, created_at, updated_at
) VALUES
    ('legacy-static-primary', 'Legacy static primary', 'fixture-static-primary-not-secret', 'bearer', 'api_key', '', '', 'any', 'any', 1, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-api-type-override', 'Legacy API-type override', 'fixture-static-fallback-not-secret', 'bearer', 'api_key', '', 'openai', 'any', 'any', 1, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    -- Equal legacy fields do not prove shared rotation intent. M1 must create
    -- independent sessions before tests establish an explicit shared reference.
    ('legacy-static-same-secret-a', 'Legacy same-secret static A', 'fixture-static-same-secret-not-secret', 'bearer', 'api_key', '', 'openai', 'vendor', 'vendor', 1, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-same-secret-b', 'Legacy same-secret static B', 'fixture-static-same-secret-not-secret', 'bearer', 'api_key', '', 'openai', 'vendor', 'vendor', 1, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-delete-target', 'Legacy deletion target', 'fixture-static-delete-not-secret', 'x-api-key', 'api_key', '', 'anthropic', 'none', 'any', 0, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-primary', 'Legacy ChatGPT primary', '', 'bearer', 'chatgpt', '', 'openai', 'vendor', 'vendor', 1, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-duplicate-owner', 'Legacy duplicate owner', '', 'bearer', 'chatgpt', '', 'openai', 'vendor', 'vendor', 1, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-duplicate-repair', 'Legacy duplicate repair', '', 'bearer', 'chatgpt', '', 'openai', 'vendor', 'vendor', 0, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00');

INSERT INTO provider_api_types (provider_id, api_type, base_url, api_key) VALUES
    ('legacy-static-primary', 'codex', 'https://static-primary.example.test', ''),
    ('legacy-static-api-type-override', 'codex', 'https://override.example.test', 'fixture-static-override-not-secret'),
    ('legacy-static-api-type-override', 'claude', 'https://override.example.test', ''),
    ('legacy-static-same-secret-a', 'codex', 'https://same-secret-static.example.test', ''),
    ('legacy-static-same-secret-b', 'codex', 'https://same-secret-static.example.test', ''),
    ('legacy-static-delete-target', 'claude', 'https://delete-target.example.test', ''),
    ('legacy-chatgpt-primary', 'codex', 'https://chatgpt.example.test', ''),
    ('legacy-chatgpt-duplicate-owner', 'codex', 'https://chatgpt.example.test', ''),
    ('legacy-chatgpt-duplicate-repair', 'codex', 'https://chatgpt.example.test', '');

INSERT INTO provider_credentials (
    provider_id, secret_data, binding_account_id, version, created_at, updated_at
) VALUES
    ('legacy-chatgpt-primary', '{"access_token":"fixture-access-primary-not-secret","refresh_token":"fixture-refresh-primary-not-secret","id_token":"fixture-id-primary-not-secret","oauth_issuer":"https://issuer.example.test","oauth_client_id":"fixture-client-primary"}', 'fixture-account-primary', 4, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-duplicate-owner', '{"access_token":"fixture-access-owner-not-secret","refresh_token":"fixture-refresh-owner-not-secret","id_token":"fixture-id-owner-not-secret","oauth_issuer":"https://issuer.example.test","oauth_client_id":"fixture-client-owner"}', 'fixture-account-duplicate', 7, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-duplicate-repair', '{"access_token":"fixture-access-repair-not-secret","refresh_token":"fixture-refresh-repair-not-secret","id_token":"fixture-id-repair-not-secret","oauth_issuer":"https://issuer.example.test","oauth_client_id":"fixture-client-repair"}', NULL, 2, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00');

INSERT INTO provider_auth_states (
    provider_id, status, status_reason, last_error, last_transition_at,
    email, account_id, plan_type, expires_at, last_refresh_at,
    usage_snapshot, refresh_fail_count, last_refresh_failure_at, created_at, updated_at
) VALUES
    ('legacy-static-primary', 'active', '', '', NULL, '', '', '', NULL, NULL, NULL, 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-api-type-override', 'active', '', '', NULL, '', '', '', NULL, NULL, NULL, 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-same-secret-a', 'active', '', '', NULL, '', '', '', NULL, NULL, NULL, 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-same-secret-b', 'active', '', '', NULL, '', '', '', NULL, NULL, NULL, 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-static-delete-target', 'active', '', '', NULL, '', '', '', NULL, NULL, NULL, 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-primary', 'active', '', '', '2026-01-02 03:04:05+00:00', 'fixture-primary@example.test', 'fixture-account-primary', 'team', '2026-01-02 04:04:05+00:00', '2026-01-02 03:00:05+00:00', '{"plan_type":"team"}', 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-duplicate-owner', 'active', '', '', '2026-01-02 03:04:05+00:00', 'fixture-owner@example.test', 'fixture-account-duplicate', 'plus', '2026-01-02 04:04:05+00:00', '2026-01-02 03:00:05+00:00', NULL, 0, NULL, '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00'),
    ('legacy-chatgpt-duplicate-repair', 'reauth_required', 'legacy_duplicate_account_binding', 'legacy credential binding is already owned by another provider; reauthentication required', '2026-01-02 03:04:05+00:00', 'fixture-repair@example.test', 'fixture-account-duplicate', 'plus', '2026-01-02 04:04:05+00:00', '2026-01-02 02:59:05+00:00', NULL, 1, '2026-01-02 03:03:05+00:00', '2026-01-02 03:04:05+00:00', '2026-01-02 03:04:05+00:00');

COMMIT;
