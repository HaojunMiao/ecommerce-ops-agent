-- Prompt:不可变模板版本。发布由 AgentEnv 统一管理。

-- name: GetPrompt :one
SELECT * FROM prompts WHERE id = $1 LIMIT 1;

-- name: ListPrompts :many
SELECT * FROM prompts WHERE workspace_id = $1 ORDER BY created_at;

-- name: CreatePrompt :one
INSERT INTO prompts (id, workspace_id, name, category, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, now(), now())
RETURNING *;

-- name: GetPromptVersion :one
SELECT * FROM prompt_versions WHERE id = $1 LIMIT 1;

-- name: GetPromptVersionByNumber :one
SELECT * FROM prompt_versions WHERE prompt_id = $1 AND version = $2 LIMIT 1;

-- name: ListPromptVersions :many
SELECT * FROM prompt_versions WHERE prompt_id = $1 ORDER BY version;

-- name: CreatePromptVersion :one
INSERT INTO prompt_versions (id, prompt_id, version, template, variables_schema, hash, token_estimate, created_by, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
RETURNING *;
