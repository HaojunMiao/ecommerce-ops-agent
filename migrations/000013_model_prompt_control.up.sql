-- 000013_model_prompt_control:
-- 模型账号、Prompt 原子配置版本与会话运行快照。

ALTER TABLE providers
    ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN name TEXT NOT NULL DEFAULT '',
    ADD COLUMN api_key_ciphertext BYTEA,
    ADD COLUMN created_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE UNIQUE INDEX providers_workspace_name
    ON providers (workspace_id, name)
    WHERE workspace_id <> '' AND name <> '';

CREATE TABLE model_deployments (
    id                  UUID PRIMARY KEY,
    workspace_id        TEXT NOT NULL,
    provider_account_id UUID NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    model_name          TEXT NOT NULL,
    region              TEXT NOT NULL DEFAULT '',
    timeout_ms          INT NOT NULL DEFAULT 30000,
    max_retries         INT NOT NULL DEFAULT 1,
    status              TEXT NOT NULL DEFAULT 'active',
    created_by          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE model_profiles (
    id           UUID PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE model_profile_versions (
    id                      UUID PRIMARY KEY,
    profile_id              UUID NOT NULL REFERENCES model_profiles(id) ON DELETE CASCADE,
    version                 INT NOT NULL,
    primary_deployment_id   UUID NOT NULL REFERENCES model_deployments(id),
    created_by              TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (profile_id, version)
);

CREATE TABLE prompt_version_configs (
    prompt_version_id       UUID PRIMARY KEY REFERENCES prompt_versions(id) ON DELETE CASCADE,
    model_profile_version_id UUID REFERENCES model_profile_versions(id),
    generation_config       JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE conversation_runtime_configs (
    conversation_id UUID PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    config_json     JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE model_call_logs
    ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN prompt_version_id UUID,
    ADD COLUMN model_profile_version_id UUID,
    ADD COLUMN deployment_id UUID;
