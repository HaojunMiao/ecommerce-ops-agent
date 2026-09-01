// Command server 是电商运营 Agent 平台入口：读配置、装配依赖、启动 HTTP/SSE 并优雅退出。

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/jobs"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/otel"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres"
	redisinfra "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/redis"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/cache"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/engine"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
)

// @title           E-commerce Operations Agent API
// @version         1.0
// @description     跨境电商运营 Agent REST API，由 swaggo/swag 从 handler 注解生成。
// @BasePath        /api/v1
// @securityDefinitions.apikey  BearerAuth
// @in              header
// @name            Authorization
func main() {
	cfg := config.Load()
	cfg.MustValidate() // 必填配置启动时就校验（快速失败）

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 基础设施装配
	log.Println("Initializing infrastructure...")

	// 1. OpenTelemetry
	otelCleanup := otel.MustInit(ctx, otel.Config{
		Endpoint: cfg.OTLPEndpoint, Headers: cfg.OTLPHeaders,
		ServiceName: "ecommerce-ops-agent-server", ServiceVersion: cfg.ServiceVersion,
		Environment: cfg.Environment, SampleRatio: cfg.OTELSampleRatio,
	})
	defer otelCleanup()

	// 2. 数据库
	db := postgres.MustOpen(ctx, cfg.DatabaseURL)
	defer db.Close()

	// 3. Redis
	rds := redisinfra.MustOpen(ctx, cfg.RedisURL)
	defer rds.Close()

	// 4. 任务队列客户端
	jobsClient := jobs.NewClient(rds)
	defer jobsClient.Close()

	// 平台服务装配。
	log.Println("Initializing platform services...")
	// Embedding 使用独立出口，避免与聊天模型供应商和密钥耦合。
	embedder, err := retriever.NewEmbedder(cfg.EmbedderKind, cfg.EmbedderDim, cfg.EmbedderBaseURL, cfg.EmbedderAPIKey, cfg.EmbedderModel)
	must(err)
	var reranker retriever.Reranker
	if cfg.RerankerEnabled {
		reranker, err = retriever.NewSiliconFlowReranker(
			cfg.RerankerBaseURL, cfg.RerankerAPIKey, cfg.RerankerModel,
		)
		must(err)
		log.Printf("Reranker enabled: model=%s candidate_k=%d", cfg.RerankerModel, cfg.RerankerCandidateK)
	}
	// jobsClient 作为 KB ingest 的异步投递器:SyncMarkdownFolder → 入队 → worker 落 kb_chunks。
	platformService := platform.NewServiceWithReranker(
		db, rds, cfg.JWTKeyBytes(), embedder, jobsClient,
		reranker, cfg.RerankerCandidateK, []byte(cfg.CredentialEncryptionKey),
	)
	endpointPolicy := tool.NewEndpointPolicy(cfg.ToolAllowedHosts, cfg.ToolAllowPrivateNetwork)
	platformService.Tool.ConfigureEndpointPolicy(endpointPolicy)
	platformService.ModelConfig.ConfigureEndpointPolicy(endpointPolicy)
	platformService.ModelConfig.SetCredential(modelconfig.DefaultCredentialRef, cfg.LLMAPIKey)
	platformService.KB.ConfigureMarkdownAllowedRoots(cfg.KBMarkdownAllowedRoots)
	if err := platformService.Tool.MigrateLegacyCredentials(ctx); err != nil {
		log.Fatalf("migrate legacy tool credentials: %v", err)
	}

	// 首启自动 seed admin:让 `make up && open localhost:8080` 直接能登录(make seed 沦为可选)
	if cfg.AutoseedAdmin {
		if err := platformService.IAM.EnsureSeedAdmin(ctx, cfg.AutoseedAdminEmail, cfg.AutoseedAdminPassword); err != nil {
			log.Printf("autoseed admin: %v", err)
		} else {
			log.Printf("✅ admin ready: %s", cfg.AutoseedAdminEmail)
		}
		if err := platformService.IAM.EnsureSeedWorkspaces(ctx); err != nil {
			log.Printf("autoseed workspaces: %v", err)
		} else {
			log.Printf("✅ default business workspaces ready")
		}
		if err := platformService.IAM.EnsureSeedWorkspaceOwners(ctx, cfg.AutoseedAdminEmail); err != nil {
			log.Printf("autoseed workspace owners: %v", err)
		}
		if cfg.LLMAPIKey != "" {
			if err := ensureDefaultModelConfigs(ctx, platformService, cfg); err != nil {
				log.Printf("autoseed model configs: %v", err)
			} else {
				log.Printf("✅ default workspace model configs ready")
			}
		}
	}

	// Runtime装配
	log.Println("Initializing runtime...")
	llmGateway := llm.NewGateway()
	llmGateway.WithConfigResolver(platformService.ModelConfig)
	// 模型调用计量包含 Prompt Cache 命中量。
	if db != nil {
		llmGateway.WithCallSink(llm.NewPgModelCallSink(db))
	}
	llmGateway.WithEndpointPolicy(endpointPolicy)

	runtime := engine.NewEngine(platformService.Agent, llmGateway, platformService.Registry).
		WithGuard(platformService.Guard).
		WithAudit(platformService.Audit).
		WithToolAudit(platformService.Tool).
		WithApprovals(platformService.ApprovalGate()).
		WithTracing(engine.TraceOptions{CaptureContent: cfg.OTELCaptureContent})
	defer platformService.Audit.Close()

	// HTTP服务装配
	log.Println("Initializing HTTP server...")
	handler := api.NewHandler(platformService, runtime, jobsClient).
		SetAllowedOrigins(cfg.CORSAllowedOrigins).
		SetIdempotencyStore(cache.NewRedisIdemStore(rds)).
		SetObservability(api.ObservabilityConfig{
			OTLPEndpoint: cfg.OTLPEndpoint, LangfuseUIURL: cfg.LangfuseUIURL,
			LangfuseProjectID: cfg.LangfuseProjectID,
		}).
		SetReadiness(
			api.ReadinessCheck{Name: "postgres", Check: db.Ping},
			api.ReadinessCheck{Name: "redis", Check: func(ctx context.Context) error { return rds.Ping(ctx).Err() }},
		)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// SSE 是长连接，因此不设置整体 WriteTimeout。
	}

	// 启动HTTP服务
	go func() {
		log.Printf("e-commerce operations agent server listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// 等待停止信号
	<-ctx.Done()
	log.Println("shutting down...")

	// 优雅关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	log.Println("server stopped")
}

func ensureDefaultModelConfigs(ctx context.Context, services *platform.Service, cfg config.Config) error {
	workspaces, err := services.IAM.ListWorkspaces(ctx, 200, 0)
	if err != nil {
		return err
	}
	workspaceByName := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		workspaceByName[workspace.Name] = workspace.ID
	}
	seeds := []struct {
		workspaceName string
		configName    string
	}{
		{
			workspaceName: "跨境电商运营平台",
			configName:    "默认模型配置",
		},
	}
	for _, seed := range seeds {
		workspaceID := workspaceByName[seed.workspaceName]
		if workspaceID == "" {
			return fmt.Errorf("default workspace %q not found", seed.workspaceName)
		}
		if _, err := services.ModelConfig.EnsureConfigVersion(ctx, modelconfig.EnsureConfigRequest{
			WorkspaceID: workspaceID, Name: seed.configName, ProviderKind: "openai-compatible",
			BaseURL: cfg.LLMBaseURL, ModelName: cfg.LLMModel,
			CredentialRef: modelconfig.DefaultCredentialRef,
			TimeoutMS:     cfg.LLMTimeoutMS, MaxRetries: cfg.LLMMaxRetries,
			InputPricePerMillion:       cfg.LLMInputPricePerMillion,
			OutputPricePerMillion:      cfg.LLMOutputPricePerMillion,
			CachedInputPricePerMillion: cfg.LLMCachedInputPricePerMillion,
			CreatedBy:                  "system",
		}); err != nil {
			return fmt.Errorf("seed model config for %q: %w", seed.workspaceName, err)
		}
	}
	return nil
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
