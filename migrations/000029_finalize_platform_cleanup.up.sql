-- 为已经运行过旧迁移链的数据库清理最后一批只读遗留表。
DROP TABLE IF EXISTS prompt_usage_logs CASCADE;
DROP TABLE IF EXISTS pii_policies CASCADE;
DROP TABLE IF EXISTS tool_permissions CASCADE;
DROP TABLE IF EXISTS usage_metrics CASCADE;
DROP TABLE IF EXISTS routing_policies CASCADE;
DROP TABLE IF EXISTS model_aliases CASCADE;

ALTER TABLE model_profile_versions
    DROP COLUMN IF EXISTS fallback_deployment_ids,
    DROP COLUMN IF EXISTS classification_max;

ALTER TABLE tools
    DROP COLUMN IF EXISTS classification_max;

DELETE FROM tools WHERE source_type NOT IN ('rest_api', 'internal_sdk');
ALTER TABLE tools DROP CONSTRAINT IF EXISTS tools_source_type_check;
ALTER TABLE tools
    ADD CONSTRAINT tools_source_type_check
    CHECK (source_type IN ('rest_api', 'internal_sdk'));
