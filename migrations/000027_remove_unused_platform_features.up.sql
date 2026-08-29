-- 收敛到单 Agent 电商运营主线：移除未接入业务的通用平台能力。
DROP TABLE IF EXISTS project_model_usage_reservations CASCADE;
DROP TABLE IF EXISTS project_model_bindings CASCADE;
DROP TABLE IF EXISTS quota_ledger CASCADE;
DROP TABLE IF EXISTS prompt_usage_logs CASCADE;
DROP TABLE IF EXISTS guard_rules CASCADE;
DROP TABLE IF EXISTS injection_logs CASCADE;
DROP TABLE IF EXISTS pii_policies CASCADE;
DROP TABLE IF EXISTS prompt_rollout_events CASCADE;
DROP TABLE IF EXISTS prompt_experiments CASCADE;
ALTER TABLE model_call_logs
    DROP COLUMN IF EXISTS experiment_id,
    DROP COLUMN IF EXISTS experiment_variant;
DROP TABLE IF EXISTS eval_scores CASCADE;
DROP TABLE IF EXISTS eval_runs CASCADE;
DROP TABLE IF EXISTS eval_cases CASCADE;
DROP TABLE IF EXISTS eval_datasets CASCADE;
DROP TABLE IF EXISTS judges CASCADE;
DROP TABLE IF EXISTS team_envs CASCADE;
DROP TABLE IF EXISTS team_versions CASCADE;
DROP TABLE IF EXISTS teams CASCADE;
DROP TABLE IF EXISTS agent_endpoints CASCADE;
DROP TABLE IF EXISTS sandbox_executions CASCADE;
DROP TABLE IF EXISTS tool_permissions CASCADE;
DROP TABLE IF EXISTS usage_metrics CASCADE;
DROP TABLE IF EXISTS routing_policies CASCADE;
DROP TABLE IF EXISTS model_aliases CASCADE;

DELETE FROM tools WHERE source_type NOT IN ('rest_api', 'internal_sdk');
ALTER TABLE tools DROP CONSTRAINT IF EXISTS tools_source_type_check;
ALTER TABLE tools
    ADD CONSTRAINT tools_source_type_check
    CHECK (source_type IN ('rest_api', 'internal_sdk'));
