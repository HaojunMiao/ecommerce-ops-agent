-- 000008_llm_gateway:模型提供方与调用记录。
-- model_call_logs 按 created_at 月度分区，default 分区承接超出预创建范围的数据。
-- worker 的 maintenance 任务负责创建后续分区。见 ADR 0007。

CREATE TABLE providers (
    id             UUID PRIMARY KEY,
    kind           TEXT NOT NULL,                          -- OpenAI-compatible provider
    credential_ref TEXT NOT NULL DEFAULT '',
    base_url       TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE model_call_logs (
    id            UUID NOT NULL DEFAULT gen_random_uuid(),
    agent_id      UUID,
    user_id       UUID,
    provider_id   UUID,
    model         TEXT NOT NULL DEFAULT '',
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cached_tokens INT NOT NULL DEFAULT 0,
    cost          NUMERIC NOT NULL DEFAULT 0,
    latency_ms    INT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT '',
    classification TEXT NOT NULL DEFAULT 'internal',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE TABLE model_call_logs_default PARTITION OF model_call_logs DEFAULT;
CREATE INDEX model_call_logs_agent ON model_call_logs (agent_id, created_at DESC);
