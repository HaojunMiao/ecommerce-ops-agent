ALTER TABLE model_call_logs
    DROP COLUMN IF EXISTS deployment_id,
    DROP COLUMN IF EXISTS model_profile_version_id,
    DROP COLUMN IF EXISTS prompt_version_id,
    DROP COLUMN IF EXISTS workspace_id;

DROP TABLE IF EXISTS conversation_runtime_configs;
DROP TABLE IF EXISTS prompt_version_configs;
DROP TABLE IF EXISTS model_profile_versions;
DROP TABLE IF EXISTS model_profiles;
DROP TABLE IF EXISTS model_deployments;

DROP INDEX IF EXISTS providers_workspace_name;
ALTER TABLE providers
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS created_at,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS api_key_ciphertext,
    DROP COLUMN IF EXISTS name,
    DROP COLUMN IF EXISTS workspace_id;
