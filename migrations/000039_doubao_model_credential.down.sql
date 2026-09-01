UPDATE model_config_versions
SET name = '默认模型配置', credential_ref = 'KBOT_LLM_API_KEY'
WHERE (name = 'Doubao' AND credential_ref = 'DOUBAO_API_KEY')
   OR (name = 'DeepSeek' AND credential_ref = 'DEEPSEEK_API_KEY');

ALTER TABLE model_config_versions
    ALTER COLUMN credential_ref SET DEFAULT 'KBOT_LLM_API_KEY';
