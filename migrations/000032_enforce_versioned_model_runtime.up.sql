-- New runtime data must carry one immutable model configuration version end to end.
-- NOT VALID preserves inspectability of legacy rows while enforcing the invariant for every
-- newly inserted or updated row. Legacy rows without a pinned version fail explicitly in the
-- service layer and can be migrated deliberately instead of silently using deployment defaults.
ALTER TABLE prompt_version_configs
    ADD CONSTRAINT prompt_version_configs_model_config_required
    CHECK (model_config_version_id IS NOT NULL) NOT VALID;

ALTER TABLE agent_versions
    ADD CONSTRAINT agent_versions_model_config_required
    CHECK (NULLIF(snapshot_json::jsonb ->> 'model_config_version_id', '') IS NOT NULL) NOT VALID;

ALTER TABLE conversation_runtime_configs
    ADD CONSTRAINT conversation_runtime_model_config_required
    CHECK (NULLIF(config_json ->> 'model_config_version_id', '') IS NOT NULL) NOT VALID;

ALTER TABLE model_call_logs
    ADD CONSTRAINT model_call_logs_model_config_required
    CHECK (model_config_version_id IS NOT NULL) NOT VALID;
