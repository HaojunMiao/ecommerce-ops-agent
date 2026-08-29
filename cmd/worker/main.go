// Command worker 运行 KB 入库与审批续跑任务。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/hibiken/asynq"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/jobs"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/otel"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres"
	redisinfra "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/redis"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/kb"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/engine"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
)

func main() {
	cfg := config.Load()
	cfg.MustValidate()
	ctx := context.Background()
	otelCleanup := otel.MustInit(ctx, otel.Config{
		Endpoint: cfg.OTLPEndpoint, Headers: cfg.OTLPHeaders,
		ServiceName: "ecommerce-ops-agent-worker", ServiceVersion: cfg.ServiceVersion,
		Environment: cfg.Environment, SampleRatio: cfg.OTELSampleRatio,
	})
	defer otelCleanup()

	db := postgres.MustOpen(ctx, cfg.DatabaseURL)
	defer db.Close()
	rds := redisinfra.MustOpen(ctx, cfg.RedisURL)
	defer rds.Close()

	embedder, err := retriever.NewEmbedder(cfg.EmbedderKind, cfg.EmbedderDim, cfg.EmbedderBaseURL, cfg.EmbedderAPIKey, cfg.EmbedderModel)
	if err != nil {
		log.Fatalf("build embedder: %v", err)
	}
	plat := platform.NewService(db, rds, cfg.JWTKeyBytes(), prompt.NoopPublisher{}, embedder, nil,
		[]byte(cfg.CredentialEncryptionKey))
	endpointPolicy := tool.NewEndpointPolicy(cfg.ToolAllowedHosts, cfg.ToolAllowPrivateNetwork)
	plat.Tool.ConfigureEndpointPolicy(endpointPolicy)
	plat.ModelConfig.ConfigureEndpointPolicy(endpointPolicy)
	plat.ModelConfig.SetCredential(modelconfig.DefaultCredentialRef, cfg.LLMAPIKey)
	plat.KB.ConfigureMarkdownAllowedRoots(cfg.KBMarkdownAllowedRoots)
	defer plat.Audit.Close()

	// 定期扫描已批准但尚未投递的恢复任务，避免瞬时队列故障丢失续跑。
	scheduler := jobs.NewScheduler(rds)
	if _, err := scheduler.Register("@every 15s", asynq.NewTask(jobs.TypeApprovalResumeDispatch, nil)); err != nil {
		log.Printf("register approval resume dispatch: %v", err)
	}
	go func() {
		if err := scheduler.Run(); err != nil {
			log.Printf("scheduler stopped: %v", err)
		}
	}()

	// 审批通过后的会话由 worker 恢复执行。
	llmGateway := llm.NewGateway()
	llmGateway.WithEndpointPolicy(endpointPolicy)
	llmGateway.WithConfigResolver(plat.ModelConfig)
	llmGateway.WithCallSink(llm.NewPgModelCallSink(db))
	resumeEngine := engine.NewEngine(plat.Agent, llmGateway, plat.Registry).
		WithGuard(plat.Guard).WithAudit(plat.Audit).WithToolAudit(plat.Tool).WithApprovals(plat.ApprovalGate()).
		WithTracing(engine.TraceOptions{CaptureContent: cfg.OTELCaptureContent})

	server := jobs.NewServer(rds, 10)
	jobsClient := jobs.NewClient(rds)
	defer jobsClient.Close()
	server.HandleFunc(kb.TypeIngestDocument, plat.KB.HandleIngest())
	server.HandleFunc(jobs.TypeEngineResume, func(ctx context.Context, t *asynq.Task) error {
		var p jobs.ResumePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		if p.ConversationID == "" || p.ApprovalID == "" {
			return fmt.Errorf("engine_resume requires conversation_id and approval_id")
		}
		_, err := resumeEngine.Resume(ctx, p.ConversationID, p.ApprovalID)
		if errors.Is(err, approval.ErrExecutionUnavailable) {
			return nil
		}
		return err
	})
	server.HandleFunc(jobs.TypeApprovalResumeDispatch, func(ctx context.Context, _ *asynq.Task) error {
		ready, err := plat.Approvals.ListReadyResumes(ctx, 100)
		if err != nil {
			return err
		}
		for _, item := range ready {
			payload, err := json.Marshal(jobs.ResumePayload{ConversationID: item.ConversationID, ApprovalID: item.ID})
			if err != nil {
				return err
			}
			_, err = jobsClient.Enqueue(
				asynq.NewTask(jobs.TypeEngineResume, payload),
				asynq.TaskID("approval-resume-"+strings.ReplaceAll(item.ID, "-", "")),
			)
			if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
				return err
			}
		}
		return nil
	})
	log.Println("worker started, consuming KB ingest and approval resume jobs...")
	if err := server.Start(); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}
