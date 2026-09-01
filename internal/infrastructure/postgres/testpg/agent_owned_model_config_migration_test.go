//go:build integration

package testpg

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

// TestAgentOwnedModelConfigMigration_BackfillsLegacySnapshot guards the only
// stateful part of migration 37: existing Prompt-owned model/generation data and
// a User Prompt environment pointer must become immutable AgentVersion fields.
func TestAgentOwnedModelConfigMigration_BackfillsLegacySnapshot(t *testing.T) {
	pool := Start(t)
	ctx := context.Background()

	dir, err := findMigrationsDir()
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	dbURL := pool.Config().ConnConfig.ConnString()
	if strings.HasPrefix(dbURL, "postgres://") {
		dbURL = "pgx5://" + strings.TrimPrefix(dbURL, "postgres://")
	}
	m, err := migrate.New("file://"+dir, dbURL)
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	defer m.Close()
	if err := m.Migrate(36); err != nil {
		t.Fatalf("migrate to legacy schema: %v", err)
	}

	const (
		systemPromptID  = "00000000-0000-0000-0000-000000000101"
		systemVersionID = "00000000-0000-0000-0000-000000000102"
		userPromptID    = "00000000-0000-0000-0000-000000000201"
		userVersionID   = "00000000-0000-0000-0000-000000000202"
		modelConfigID   = "00000000-0000-0000-0000-000000000301"
		agentID         = "00000000-0000-0000-0000-000000000401"
		agentVersionID  = "00000000-0000-0000-0000-000000000402"
	)
	legacySnapshot := `{
		"system_prompt_version_id":"` + systemVersionID + `",
		"model_config_version_id":"` + modelConfigID + `",
		"user_prompt_id":"` + userPromptID + `",
		"prompt_env":"prod",
		"tool_version_ids":[],"skill_version_ids":[],"kb_ids":[]
	}`

	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO prompts (id, workspace_id, name, category) VALUES
			($1, 'ws-migration', 'system', 'system'), ($2, 'ws-migration', 'user', 'user')`,
			[]any{systemPromptID, userPromptID}},
		{`INSERT INTO prompt_versions (id, prompt_id, version, template) VALUES
			($1, $2, 1, 'system template'), ($3, $4, 1, 'user {{.question}}')`,
			[]any{systemVersionID, systemPromptID, userVersionID, userPromptID}},
		{`INSERT INTO prompt_envs (prompt_id, env, version_id) VALUES ($1, 'prod', $2)`,
			[]any{userPromptID, userVersionID}},
		{`INSERT INTO model_config_versions
			(id, workspace_id, name, version, base_url, model_name)
			VALUES ($1, 'ws-migration', 'model', 1, 'https://example.invalid/v1', 'test-model')`,
			[]any{modelConfigID}},
		{`INSERT INTO prompt_version_configs
			(prompt_version_id, model_config_version_id, generation_config)
			VALUES ($1, $2, '{"temperature":0.2,"max_output_tokens":321}'::jsonb)`,
			[]any{systemVersionID, modelConfigID}},
		{`INSERT INTO agents (id, workspace_id, name) VALUES ($1, 'ws-migration', 'agent')`,
			[]any{agentID}},
		{`INSERT INTO agent_versions (id, agent_id, version, snapshot_json) VALUES ($1, $2, 1, $3)`,
			[]any{agentVersionID, agentID, legacySnapshot}},
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed legacy data: %v", err)
		}
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 37: %v", err)
	}

	var (
		systemVersion string
		userVersion   string
		modelConfig   string
		temperature   string
		maxTokens     string
		legacyKeys    bool
		promptEnvs    *string
		promptConfigs *string
	)
	err = pool.QueryRow(ctx, `
		SELECT
			snapshot_json::jsonb ->> 'system_prompt_version_id',
			snapshot_json::jsonb ->> 'user_prompt_version_id',
			snapshot_json::jsonb ->> 'model_config_version_id',
			snapshot_json::jsonb -> 'generation_config' ->> 'temperature',
			snapshot_json::jsonb -> 'generation_config' ->> 'max_output_tokens',
			snapshot_json::jsonb ?| ARRAY['system_prompt_id','user_prompt_id','prompt_env'],
			to_regclass('public.prompt_envs')::text,
			to_regclass('public.prompt_version_configs')::text
		FROM agent_versions WHERE id = $1
	`, agentVersionID).Scan(&systemVersion, &userVersion, &modelConfig,
		&temperature, &maxTokens, &legacyKeys, &promptEnvs, &promptConfigs)
	if err != nil {
		t.Fatalf("read migrated snapshot: %v", err)
	}
	if systemVersion != systemVersionID || userVersion != userVersionID || modelConfig != modelConfigID {
		t.Fatalf("references not preserved: system=%s user=%s model=%s", systemVersion, userVersion, modelConfig)
	}
	if temperature != "0.2" || maxTokens != "321" {
		t.Fatalf("generation config not preserved: temperature=%s max_output_tokens=%s", temperature, maxTokens)
	}
	if legacyKeys || promptEnvs != nil || promptConfigs != nil {
		t.Fatalf("legacy state remains: keys=%v prompt_envs=%v prompt_version_configs=%v", legacyKeys, promptEnvs, promptConfigs)
	}
}
