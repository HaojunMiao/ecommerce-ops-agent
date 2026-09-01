-- 回滚仅恢复旧表结构；AgentVersion 已允许同一 PromptVersion 搭配不同模型，
-- 因而无法无损还原旧的一对一 PromptVersionConfig 归属关系。
CREATE TABLE prompt_version_configs (
    prompt_version_id       UUID PRIMARY KEY REFERENCES prompt_versions(id) ON DELETE CASCADE,
    model_config_version_id UUID NOT NULL REFERENCES model_config_versions(id),
    generation_config       JSONB NOT NULL DEFAULT '{}'
);

INSERT INTO prompt_version_configs (prompt_version_id, model_config_version_id, generation_config)
SELECT DISTINCT ON ((snapshot_json::jsonb ->> 'system_prompt_version_id')::uuid)
    (snapshot_json::jsonb ->> 'system_prompt_version_id')::uuid,
    (snapshot_json::jsonb ->> 'model_config_version_id')::uuid,
    COALESCE(snapshot_json::jsonb -> 'generation_config', '{}'::jsonb)
FROM agent_versions
WHERE COALESCE(snapshot_json::jsonb ->> 'system_prompt_version_id', '')
          ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND COALESCE(snapshot_json::jsonb ->> 'model_config_version_id', '')
          ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
ORDER BY (snapshot_json::jsonb ->> 'system_prompt_version_id')::uuid, version DESC;

CREATE TABLE prompt_envs (
    prompt_id  UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    env        TEXT NOT NULL,
    version_id UUID NOT NULL REFERENCES prompt_versions(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (prompt_id, env)
);

INSERT INTO prompt_envs (prompt_id, env, version_id)
SELECT DISTINCT ON (prompt_id) prompt_id, 'dev', id
FROM prompt_versions
ORDER BY prompt_id, version DESC;

ALTER TABLE agent_versions
    DROP CONSTRAINT IF EXISTS agent_versions_system_prompt_version_required,
    DROP CONSTRAINT IF EXISTS agent_versions_generation_config_required;
