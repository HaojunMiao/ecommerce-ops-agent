// Command bootstrap-model-config initializes the first immutable model configuration for a workspace.
// It is intentionally independent from demo/admin auto-seeding so production deployments have an
// explicit, repeatable initialization path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
)

func main() {
	cfg := config.Load()
	workspaceID := flag.String("workspace-id", "", "target workspace UUID")
	workspaceName := flag.String("workspace-name", envOr("KBOT_MODEL_CONFIG_WORKSPACE", "跨境电商运营平台"), "target workspace name when workspace-id is omitted")
	configName := flag.String("name", envOr("KBOT_MODEL_CONFIG_NAME", "默认模型配置"), "logical model configuration name")
	baseURL := flag.String("base-url", cfg.LLMBaseURL, "OpenAI-compatible model API base URL")
	modelName := flag.String("model", cfg.LLMModel, "model name")
	timeoutMS := flag.Int("timeout-ms", cfg.LLMTimeoutMS, "request timeout in milliseconds")
	maxRetries := flag.Int("max-retries", cfg.LLMMaxRetries, "maximum model retries")
	inputPrice := flag.Float64("input-price-per-million", cfg.LLMInputPricePerMillion, "input token price per million")
	outputPrice := flag.Float64("output-price-per-million", cfg.LLMOutputPricePerMillion, "output token price per million")
	cachedInputPrice := flag.Float64("cached-input-price-per-million", cfg.LLMCachedInputPricePerMillion, "cached input token price per million")
	createdBy := flag.String("created-by", "bootstrap-model-config", "audit actor")
	flag.Parse()

	if strings.TrimSpace(cfg.LLMAPIKey) == "" {
		log.Fatal("KBOT_LLM_API_KEY is required so the created credential_ref is callable")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	resolvedWorkspaceID := strings.TrimSpace(*workspaceID)
	if resolvedWorkspaceID == "" {
		if strings.TrimSpace(*workspaceName) == "" {
			log.Fatal("workspace-id or workspace-name is required")
		}
		err := db.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE name=$1`, strings.TrimSpace(*workspaceName)).Scan(&resolvedWorkspaceID)
		if err == pgx.ErrNoRows {
			log.Fatalf("workspace %q not found; create the workspace before initializing its model config", *workspaceName)
		}
		if err != nil {
			log.Fatalf("resolve workspace: %v", err)
		}
	} else {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1)`, resolvedWorkspaceID).Scan(&exists); err != nil {
			log.Fatalf("validate workspace: %v", err)
		}
		if !exists {
			log.Fatalf("workspace %q not found", resolvedWorkspaceID)
		}
	}

	service := modelconfig.NewService(modelconfig.NewPostgresStore(db))
	version, err := service.EnsureConfigVersion(ctx, modelconfig.EnsureConfigRequest{
		WorkspaceID: resolvedWorkspaceID, Name: strings.TrimSpace(*configName), ProviderKind: "openai-compatible",
		BaseURL: strings.TrimSpace(*baseURL), ModelName: strings.TrimSpace(*modelName), CredentialRef: modelconfig.DefaultCredentialRef,
		TimeoutMS: *timeoutMS, MaxRetries: *maxRetries,
		InputPricePerMillion: *inputPrice, OutputPricePerMillion: *outputPrice,
		CachedInputPricePerMillion: *cachedInputPrice,
		CreatedBy:                  strings.TrimSpace(*createdBy),
	})
	if err != nil {
		log.Fatalf("initialize model config: %v", err)
	}
	fmt.Printf("model_config_version_id=%s name=%s version=%d workspace_id=%s\n", version.ID, version.Name, version.Version, version.WorkspaceID)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
