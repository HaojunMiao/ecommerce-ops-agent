-- AgentVersion 是唯一运行配置快照。Conversation 只记录会话自身状态，
-- 不再复制或覆盖 Prompt、模型与生成参数。
ALTER TABLE conversation_runtime_configs
    DROP CONSTRAINT IF EXISTS conversation_runtime_model_config_required;

UPDATE conversation_runtime_configs
SET config_json = config_json
    - 'system_prompt'
    - 'prompt_version_id'
    - 'model_config_version_id'
    - 'generation_config';

-- AgentVersion 控制面改为直接提交不可变 ToolVersionID；旧快照已经包含
-- tool_version_ids，只需删除仅供二次解析的注册 ID。
UPDATE agent_versions
SET snapshot_json = (snapshot_json::jsonb - 'tool_ids')::text
WHERE snapshot_json::jsonb ? 'tool_ids';
