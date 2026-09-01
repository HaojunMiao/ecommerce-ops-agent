//go:build integration

package testpg

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func TestModelCredentialMigrationClassifiesLegacyConfigsByIdentity(t *testing.T) {
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
	if err := m.Migrate(38); err != nil {
		t.Fatalf("migrate to version 38: %v", err)
	}

	rows := []struct {
		id, workspace, baseURL, model string
	}{
		{"00000000-0000-0000-0000-000000000701", "ws-doubao", "https://ark.cn-beijing.volces.com/api/v3", "doubao-seed-2-1-pro-260628"},
		{"00000000-0000-0000-0000-000000000702", "ws-deepseek", "https://api.deepseek.com/v1", "deepseek-chat"},
		{"00000000-0000-0000-0000-000000000703", "ws-unknown", "https://models.example.com/v1", "custom-model"},
	}
	for _, row := range rows {
		if _, err := pool.Exec(ctx, `
			INSERT INTO model_config_versions
				(id, workspace_id, name, version, base_url, model_name, credential_ref)
			VALUES ($1, $2, '默认模型配置', 1, $3, $4, 'KBOT_LLM_API_KEY')
		`, row.id, row.workspace, row.baseURL, row.model); err != nil {
			t.Fatalf("seed %s: %v", row.workspace, err)
		}
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 39: %v", err)
	}

	want := map[string][2]string{
		"ws-doubao":   {"Doubao", "DOUBAO_API_KEY"},
		"ws-deepseek": {"DeepSeek", "DEEPSEEK_API_KEY"},
		"ws-unknown":  {"默认模型配置", "KBOT_LLM_API_KEY"},
	}
	for workspace, expected := range want {
		var name, credentialRef string
		if err := pool.QueryRow(ctx, `
			SELECT name, credential_ref FROM model_config_versions WHERE workspace_id=$1
		`, workspace).Scan(&name, &credentialRef); err != nil {
			t.Fatalf("read %s: %v", workspace, err)
		}
		if name != expected[0] || credentialRef != expected[1] {
			t.Fatalf("%s migrated to %s/%s, want %s/%s", workspace, name, credentialRef, expected[0], expected[1])
		}
	}
}
