-- Collapse Provider -> Deployment -> Profile -> ProfileVersion into one immutable config version.
-- API keys are not migrated into the new table; runtime resolves credential_ref from the environment.
CREATE TABLE model_config_versions (
    id                               UUID PRIMARY KEY,
    workspace_id                     TEXT NOT NULL,
    name                             TEXT NOT NULL,
    version                          INT NOT NULL CHECK (version > 0),
    provider_kind                    TEXT NOT NULL DEFAULT 'openai-compatible',
    base_url                         TEXT NOT NULL,
    model_name                       TEXT NOT NULL,
    credential_ref                   TEXT NOT NULL DEFAULT 'KBOT_LLM_API_KEY',
    timeout_ms                       INT NOT NULL DEFAULT 120000 CHECK (timeout_ms > 0),
    max_retries                      INT NOT NULL DEFAULT 0 CHECK (max_retries >= 0),
    input_price_per_million          NUMERIC NOT NULL DEFAULT 0 CHECK (input_price_per_million >= 0),
    output_price_per_million         NUMERIC NOT NULL DEFAULT 0 CHECK (output_price_per_million >= 0),
    cached_input_price_per_million   NUMERIC NOT NULL DEFAULT 0 CHECK (cached_input_price_per_million >= 0),
    created_by                       TEXT NOT NULL DEFAULT '',
    created_at                       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name, version)
);

-- Preserve existing IDs so Prompt/Agent/Conversation references remain stable.
INSERT INTO model_config_versions (
    id, workspace_id, name, version, provider_kind, base_url, model_name, credential_ref,
    timeout_ms, max_retries, input_price_per_million, output_price_per_million,
    cached_input_price_per_million, created_by, created_at
)
SELECT mpv.id, mp.workspace_id, mp.name, mpv.version, p.kind, p.base_url, md.model_name,
       'KBOT_LLM_API_KEY', md.timeout_ms, md.max_retries, md.input_price_per_million,
       md.output_price_per_million, md.cached_input_price_per_million, mpv.created_by, mpv.created_at
FROM model_profile_versions mpv
JOIN model_profiles mp ON mp.id = mpv.profile_id
JOIN model_deployments md ON md.id = mpv.primary_deployment_id
JOIN providers p ON p.id = md.provider_account_id;

ALTER TABLE prompt_version_configs
    DROP CONSTRAINT IF EXISTS prompt_version_configs_model_profile_version_id_fkey;
ALTER TABLE prompt_version_configs
    RENAME COLUMN model_profile_version_id TO model_config_version_id;
ALTER TABLE prompt_version_configs
    ADD CONSTRAINT prompt_version_configs_model_config_version_id_fkey
    FOREIGN KEY (model_config_version_id) REFERENCES model_config_versions(id);

ALTER TABLE model_call_logs
    RENAME COLUMN model_profile_version_id TO model_config_version_id;
ALTER TABLE model_call_logs
    DROP COLUMN IF EXISTS provider_id,
    DROP COLUMN IF EXISTS deployment_id;

-- Rename immutable JSON snapshot keys without changing their referenced IDs.
UPDATE agent_versions
SET snapshot_json = (
    (snapshot_json::jsonb - 'model_profile_version_id') ||
    CASE WHEN snapshot_json::jsonb ? 'model_profile_version_id'
         THEN jsonb_build_object('model_config_version_id', snapshot_json::jsonb -> 'model_profile_version_id')
         ELSE '{}'::jsonb END
)::text;

UPDATE conversation_runtime_configs
SET config_json = (config_json - 'model_profile_version_id') ||
    CASE WHEN config_json ? 'model_profile_version_id'
         THEN jsonb_build_object('model_config_version_id', config_json -> 'model_profile_version_id')
         ELSE '{}'::jsonb END;

DROP TABLE model_profile_versions;
DROP TABLE model_profiles;
DROP TABLE model_deployments;
DROP TABLE providers;
