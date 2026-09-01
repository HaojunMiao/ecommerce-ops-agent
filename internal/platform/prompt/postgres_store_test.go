//go:build integration

package prompt_test

// PG 版 Prompt Store 契约测试。需 Docker(或 KBOT_TEST_DATABASE_URL)。

import (
	"context"
	"testing"

	pgstore "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/sqlc"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
)

func TestPostgresPromptStore_Contract(t *testing.T) {
	pool := testpg.Start(t)
	runPromptStoreContract(t, func(t *testing.T) prompt.Store {
		if _, err := pool.Exec(context.Background(),
			`TRUNCATE prompts, prompt_versions CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return prompt.NewPostgresStore(pool, pgstore.New(pool))
	})
}

func TestPostgresPromptService_PersistsTemplateVersion(t *testing.T) {
	pool := testpg.Start(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`TRUNCATE prompt_versions, prompts CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	store := prompt.NewPostgresStore(pool, pgstore.New(pool))
	svc := prompt.NewService(store, promptcache.NewCache())
	p, created, err := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "ws-config", Name: "configured", Template: "hello", CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if created.Template != "hello" {
		t.Fatalf("returned version lost template: %+v", created)
	}
	loaded, err := store.GetPromptVersionByNumber(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("GetPromptVersionByNumber: %v", err)
	}
	if loaded.Template != "hello" {
		t.Fatalf("persisted version lost template: %+v", loaded)
	}
}

func TestPostgresPromptService_RenderExactVersion(t *testing.T) {
	pool := testpg.Start(t)
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE prompts, prompt_versions CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	ctx := context.Background()
	svc := prompt.NewService(prompt.NewPostgresStore(pool, pgstore.New(pool)), promptcache.NewCache())
	p, v, err := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "ws-default", Name: "sys", Template: "你好 {{.name}}", CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if _, err := svc.RenderByVersion(ctx, v.ID, map[string]any{"name": "世界"}); err != nil {
		t.Fatalf("RenderByVersion: %v", err)
	}
	_ = p
}
