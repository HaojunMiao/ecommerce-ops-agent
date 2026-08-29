ALTER TABLE model_call_logs
    DROP CONSTRAINT IF EXISTS model_call_logs_model_config_required;

ALTER TABLE conversation_runtime_configs
    DROP CONSTRAINT IF EXISTS conversation_runtime_model_config_required;

ALTER TABLE agent_versions
    DROP CONSTRAINT IF EXISTS agent_versions_model_config_required;

ALTER TABLE prompt_version_configs
    DROP CONSTRAINT IF EXISTS prompt_version_configs_model_config_required;
