//go:build integration

package testpg

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func TestAgentSingleRuntimeSnapshotMigration_RemovesConversationOverrides(t *testing.T) {
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
	if err := m.Steps(-1); err != nil {
		t.Fatalf("migrate to version 37: %v", err)
	}

	const (
		agentID        = "00000000-0000-0000-0000-000000000501"
		agentVersionID = "00000000-0000-0000-0000-000000000502"
		conversationID = "00000000-0000-0000-0000-000000000503"
	)
	if _, err := pool.Exec(ctx,
		`INSERT INTO agents (id, workspace_id, name) VALUES ($1, 'ws-migration', 'agent')`, agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_versions (id, agent_id, version, snapshot_json) VALUES ($1, $2, 1, $3)
	`, agentVersionID, agentID, `{
		"system_prompt_version_id":"prompt-v1",
		"model_config_version_id":"model-v1",
		"generation_config":{"temperature":0.2},
		"tool_ids":["tool-registration"],
		"tool_version_ids":["tool-version-v1"]
	}`); err != nil {
		t.Fatalf("seed agent version: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO conversations (id, agent_id, agent_version_id, user_id, workspace_id)
		VALUES ($1, $2, $3, 'user-1', 'ws-migration')
	`, conversationID, agentID, agentVersionID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO conversation_runtime_configs (conversation_id, config_json) VALUES ($1, $2::jsonb)
	`, conversationID, `{
		"environment":"prod","latest_trace_id":"trace-1",
		"system_prompt":"rendered","prompt_version_id":"prompt-v1",
		"model_config_version_id":"model-v1","generation_config":{"temperature":0.2},
		"user_prompt_version_id":"user-prompt-v1","user_prompt_variables":{"order_id":"A1"}
	}`); err != nil {
		t.Fatalf("seed conversation runtime: %v", err)
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 38: %v", err)
	}

	var (
		runtimeHasOverrides bool
		environment         string
		traceID             string
		userPromptVersion   string
		agentHasToolIDs     bool
		toolVersionID       string
		constraintExists    bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			crc.config_json ?| ARRAY['system_prompt','prompt_version_id','model_config_version_id','generation_config'],
			crc.config_json ->> 'environment', crc.config_json ->> 'latest_trace_id',
			crc.config_json ->> 'user_prompt_version_id',
			av.snapshot_json::jsonb ? 'tool_ids',
			av.snapshot_json::jsonb -> 'tool_version_ids' ->> 0,
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'conversation_runtime_model_config_required'
			)
		FROM conversation_runtime_configs crc
		JOIN conversations c ON c.id = crc.conversation_id
		JOIN agent_versions av ON av.id::text = c.agent_version_id
		WHERE crc.conversation_id = $1
	`, conversationID).Scan(&runtimeHasOverrides, &environment, &traceID, &userPromptVersion,
		&agentHasToolIDs, &toolVersionID, &constraintExists); err != nil {
		t.Fatalf("read migrated state: %v", err)
	}
	if runtimeHasOverrides || environment != "prod" || traceID != "trace-1" || userPromptVersion != "user-prompt-v1" {
		t.Fatalf("conversation runtime migration mismatch: overrides=%v env=%s trace=%s user_prompt=%s",
			runtimeHasOverrides, environment, traceID, userPromptVersion)
	}
	if agentHasToolIDs || toolVersionID != "tool-version-v1" || constraintExists {
		t.Fatalf("agent/tool migration mismatch: tool_ids=%v tool_version=%s constraint=%v",
			agentHasToolIDs, toolVersionID, constraintExists)
	}
}
