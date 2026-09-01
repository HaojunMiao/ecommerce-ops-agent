-- Agent 是唯一发布单元：PromptVersion 只保存模板，AgentVersion 直接固定
-- PromptVersion、ModelConfigVersion 与 GenerationConfig。

-- 先把旧 Agent 快照里由 System PromptVersion 间接拥有的生成参数搬入快照，
-- 并把 User Prompt 的 prompt_id + prompt_env 解析成确切版本。
WITH snapshot_dependencies AS (
    SELECT
        av.id,
        COALESCE(pvc.generation_config, '{}'::jsonb) AS generation_config,
        upe.version_id AS user_prompt_version_id
    FROM agent_versions av
    LEFT JOIN prompt_version_configs pvc
      ON pvc.prompt_version_id = CASE
          WHEN COALESCE(av.snapshot_json::jsonb ->> 'system_prompt_version_id', '')
               ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          THEN (av.snapshot_json::jsonb ->> 'system_prompt_version_id')::uuid
          ELSE NULL
      END
    LEFT JOIN prompt_envs upe
      ON upe.prompt_id = CASE
          WHEN COALESCE(av.snapshot_json::jsonb ->> 'user_prompt_id', '')
               ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          THEN (av.snapshot_json::jsonb ->> 'user_prompt_id')::uuid
          ELSE NULL
      END
     AND upe.env = COALESCE(NULLIF(av.snapshot_json::jsonb ->> 'prompt_env', ''), 'dev')
)
UPDATE agent_versions av
SET snapshot_json = (
    (
        (
            av.snapshot_json::jsonb
            - 'system_prompt'
            - 'system_prompt_id'
            - 'user_prompt_id'
            - 'prompt_env'
        )
        || jsonb_build_object('generation_config', deps.generation_config)
        || CASE
            WHEN deps.user_prompt_version_id IS NOT NULL
            THEN jsonb_build_object('user_prompt_version_id', deps.user_prompt_version_id)
            ELSE '{}'::jsonb
           END
    )::text
)
FROM snapshot_dependencies deps
WHERE deps.id = av.id;

ALTER TABLE agent_versions
    ADD CONSTRAINT agent_versions_system_prompt_version_required
    CHECK (NULLIF(snapshot_json::jsonb ->> 'system_prompt_version_id', '') IS NOT NULL) NOT VALID,
    ADD CONSTRAINT agent_versions_generation_config_required
    CHECK (snapshot_json::jsonb ? 'generation_config') NOT VALID;

DROP TABLE prompt_envs;
DROP TABLE prompt_version_configs;
