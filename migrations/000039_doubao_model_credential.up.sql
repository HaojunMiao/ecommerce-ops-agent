-- 按实际模型身份迁移历史“默认模型配置”，不能仅凭展示名称判断供应商。
-- 保留 ModelConfigVersion ID，因此现有 AgentVersion 快照无需重写。
UPDATE model_config_versions
SET name = 'Doubao', credential_ref = 'DOUBAO_API_KEY'
WHERE name = '默认模型配置'
  AND credential_ref = 'KBOT_LLM_API_KEY'
  AND (
      lower(base_url) LIKE '%volcengine%'
      OR lower(base_url) LIKE '%volces.com%'
      OR lower(model_name) LIKE 'doubao%'
  );

UPDATE model_config_versions
SET name = 'DeepSeek', credential_ref = 'DEEPSEEK_API_KEY'
WHERE name = '默认模型配置'
  AND credential_ref = 'KBOT_LLM_API_KEY'
  AND (
      lower(base_url) LIKE '%deepseek.com%'
      OR lower(model_name) LIKE 'deepseek%'
  );

ALTER TABLE model_config_versions
    ALTER COLUMN credential_ref SET DEFAULT 'DOUBAO_API_KEY';
