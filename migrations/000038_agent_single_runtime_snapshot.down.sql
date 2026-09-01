-- 回滚时从 Conversation 固定的 AgentVersion 恢复旧运行字段。
UPDATE conversation_runtime_configs crc
SET config_json = crc.config_json || jsonb_build_object(
    'prompt_version_id', COALESCE(av.snapshot_json::jsonb ->> 'system_prompt_version_id', ''),
    'model_config_version_id', COALESCE(av.snapshot_json::jsonb ->> 'model_config_version_id', ''),
    'generation_config', COALESCE(av.snapshot_json::jsonb -> 'generation_config', '{}'::jsonb)
)
FROM conversations c
JOIN agent_versions av ON av.id::text = c.agent_version_id
WHERE c.id = crc.conversation_id;

ALTER TABLE conversation_runtime_configs
    ADD CONSTRAINT conversation_runtime_model_config_required
    CHECK (NULLIF(config_json ->> 'model_config_version_id', '') IS NOT NULL) NOT VALID;

-- tool_ids 是派生的控制面字段，无法仅靠 JSON 无损恢复；旧服务仍可通过
-- tool_version_ids 反解，因此不影响回滚后的运行和编辑。
