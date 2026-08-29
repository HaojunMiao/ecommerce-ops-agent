-- 000002_prompt_apollo:Apollo 风格 Prompt 管理(设计文档 §4.2 / §5 Prompts)

CREATE TABLE prompts (
    id           UUID PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name         TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX prompts_workspace_name ON prompts (workspace_id, name);

-- prompt_versions 字段对齐既有 domain.PromptVersion(template/variables_schema/hash/token_estimate)。
-- variables_schema 用 TEXT(domain 视其为不透明 JSON Schema 字符串,避免 JSONB 规范化破坏原样回读)。
CREATE TABLE prompt_versions (
    id               UUID PRIMARY KEY,
    prompt_id        UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version          INT NOT NULL,
    template         TEXT NOT NULL,
    variables_schema TEXT NOT NULL DEFAULT '{}',
    hash             TEXT NOT NULL DEFAULT '',
    token_estimate   INT NOT NULL DEFAULT 0,
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (prompt_id, version)
);
CREATE INDEX prompt_versions_prompt_ver ON prompt_versions (prompt_id, version DESC);

CREATE TABLE prompt_envs (
    prompt_id  UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    env        TEXT NOT NULL,                          -- dev / staging / prod
    version_id UUID NOT NULL REFERENCES prompt_versions(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (prompt_id, env)
);
