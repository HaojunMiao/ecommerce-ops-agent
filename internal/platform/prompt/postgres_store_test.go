//go:build integration

package prompt_test

// PG 版 Prompt Store 契约测试。需 Docker(或 KBOT_TEST_DATABASE_URL)。

import (
	"context"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	pgstore "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/sqlc"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
)

func TestPostgresPromptStore_Contract(t *testing.T) {
	pool := testpg.Start(t)
	runPromptStoreContract(t, func(t *testing.T) prompt.Store {
		if _, err := pool.Exec(context.Background(),
			`TRUNCATE prompts, prompt_versions, prompt_envs CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return prompt.NewPostgresStore(pool, pgstore.New(pool))
	})
}

// TestPostgresPromptService_PersistsVersionConfig 防止基础版本行回填时把
// PromptVersion 上尚未写入 prompt_version_configs 的模型参数清空。
func TestPostgresPromptService_PersistsVersionConfig(t *testing.T) {
	pool := testpg.Start(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`TRUNCATE prompt_version_configs, prompt_envs, prompt_versions, prompts CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	store := prompt.NewPostgresStore(pool, pgstore.New(pool))
	models := modelconfig.NewService(modelconfig.NewPostgresStore(pool))
	modelVersion, err := models.EnsureConfigVersion(ctx, modelconfig.EnsureConfigRequest{
		WorkspaceID: "ws-config", Name: "test", BaseURL: "https://model.example/v1", ModelName: "test",
	})
	if err != nil {
		t.Fatalf("model config: %v", err)
	}
	svc := prompt.NewService(store, promptcache.NewCache(), prompt.NoopPublisher{}).WithModelConfigs(models)
	temperature := float32(0.2)
	p, created, err := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "ws-config", Name: "configured", Template: "hello", CreatedBy: "u1",
		ModelConfigVersionID: modelVersion.ID,
		GenerationConfig:     domain.GenerationConfig{Temperature: &temperature},
	})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if created.GenerationConfig.Temperature == nil || *created.GenerationConfig.Temperature != temperature {
		t.Fatalf("returned version lost generation config: %+v", created.GenerationConfig)
	}
	loaded, err := store.GetPromptVersionByNumber(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("GetPromptVersionByNumber: %v", err)
	}
	if loaded.GenerationConfig.Temperature == nil || *loaded.GenerationConfig.Temperature != temperature {
		t.Fatalf("persisted version lost generation config: %+v", loaded.GenerationConfig)
	}
}

// TestPostgresPromptService_PromoteFlow 是服务层回归:CreatePrompt 内部会 CreateVersion + Promote,
// Promote 校验 version.PromptID == promptID。PG 把 ID 规范化为 canonical UUID,若 store 不回写 canonical ID,
// Service 仍持 32-hex,归属校验会失败(docker compose 实测发现的 400)。本测试守住这条。
func TestPostgresPromptService_PromoteFlow(t *testing.T) {
	pool := testpg.Start(t)
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE prompts, prompt_versions, prompt_envs CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	ctx := context.Background()
	models := modelconfig.NewService(modelconfig.NewPostgresStore(pool))
	modelVersion, err := models.EnsureConfigVersion(ctx, modelconfig.EnsureConfigRequest{
		WorkspaceID: "ws-default", Name: "test", BaseURL: "https://model.example/v1", ModelName: "test",
	})
	if err != nil {
		t.Fatalf("model config: %v", err)
	}
	svc := prompt.NewService(prompt.NewPostgresStore(pool, pgstore.New(pool)), promptcache.NewCache(), prompt.NoopPublisher{}).WithModelConfigs(models)
	p, v, err := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "ws-default", Name: "sys", Template: "你好 {{.name}}", ModelConfigVersionID: modelVersion.ID, CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("CreatePrompt(含 CreateVersion+Promote): %v", err)
	}
	// dev 环境应已绑定到 v1,可渲染。
	if _, err := svc.Render(ctx, p.ID, prompt.EnvDev, "u1", map[string]any{"name": "世界"}); err != nil {
		t.Fatalf("Render after promote: %v", err)
	}
	_ = v
}
