// Package api 提供HTTP路由器
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/middleware"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/streaming"
	v1 "github.com/HaojunMiao/ecommerce-ops-agent/internal/api/v1"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/jobs"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/metrics"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/cache"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/engine"
)

// Handler HTTP处理器
type Handler struct {
	platform       *platform.Service
	runtime        *engine.Engine
	jobs           *jobs.Client
	observability  ObservabilityConfig
	readiness      []ReadinessCheck
	allowedOrigins []string
	idemStore      cache.IdemStore
}

// SetAllowedOrigins 配置 HTTP CORS 来源白名单。
func (h *Handler) SetAllowedOrigins(origins []string) *Handler {
	h.allowedOrigins = append([]string(nil), origins...)
	return h
}

type ObservabilityConfig struct {
	OTLPEndpoint      string
	LangfuseUIURL     string
	LangfuseProjectID string
}

// ReadinessCheck 描述一个影响服务接流量能力的依赖检查。
type ReadinessCheck struct {
	Name  string
	Check func(context.Context) error
}

// NewHandler 创建HTTP处理器
func NewHandler(platform *platform.Service, runtime *engine.Engine, jobs *jobs.Client) *Handler {
	return &Handler{
		platform:       platform,
		runtime:        runtime,
		jobs:           jobs,
		allowedOrigins: []string{"http://localhost:8080", "http://localhost:5173"},
		idemStore:      cache.NewMemoryIdemStore(),
	}
}

// SetIdempotencyStore 注入跨进程幂等与入站事件去重存储。
func (h *Handler) SetIdempotencyStore(store cache.IdemStore) *Handler {
	if store != nil {
		h.idemStore = store
	}
	return h
}

// SetObservability 注入可观测端点配置(OTLP/Langfuse),供 GET /api/v1/observability 暴露给 Admin 页。
func (h *Handler) SetObservability(cfg ObservabilityConfig) *Handler {
	h.observability = cfg
	return h
}

// SetReadiness 注入数据库、缓存等就绪检查。
func (h *Handler) SetReadiness(checks ...ReadinessCheck) *Handler {
	h.readiness = checks
	return h
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	for _, check := range h.readiness {
		if check.Check == nil {
			continue
		}
		if err := check.Check(ctx); err != nil {
			http.Error(w, fmt.Sprintf("not ready: %s", check.Name), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Routes 创建路由
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()

	// 基础中间件
	r.Use(middleware.Recover())
	r.Use(middleware.TraceHTTP())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   h.allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key", "X-CSRF-Token", "X-Workspace-ID"},
		ExposedHeaders:   []string{"Link", "X-Request-ID", "X-Idempotent-Replay"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// /health 保留历史路径；/healthz 仅检查进程存活；/readyz 检查外部依赖。
	healthOK := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	r.Get("/health", healthOK)
	r.Get("/healthz", healthOK)
	r.Get("/readyz", h.ready)

	// Prometheus 指标。
	r.Handle("/metrics", metrics.Handler())

	// API v1路由
	r.Route("/api/v1", func(r chi.Router) {
		// 认证路由（无需认证）
		authHandler := v1.NewAuthHandler(h.platform.IAM)
		r.Post("/auth/login", authHandler.Login)

		// 需要认证的路由
		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(h.platform.IAM))
			r.Use(middleware.Workspace(h.platform.IAM))
			// 带 Idempotency-Key 的 POST/PUT 请求会进行去重。
			r.Use(middleware.Idempotency(h.idemStore))

			// IAM：用户与工作空间。
			r.With(middleware.RequireGlobalAdmin()).Post("/auth/register", authHandler.CreateUser)
			iamHandler := v1.NewIAMHandler(h.platform.IAM)
			r.With(middleware.RequireGlobalAdmin()).Get("/users", iamHandler.ListUsers)
			r.Get("/workspaces", iamHandler.ListWorkspaces)
			r.Post("/workspaces", iamHandler.CreateWorkspace)
			r.Get("/workspaces/{workspace_id}/members", iamHandler.ListWorkspaceMembers)
			r.Put("/workspaces/{workspace_id}/members/{user_id}", iamHandler.UpsertWorkspaceMember)
			r.Delete("/workspaces/{workspace_id}/members/{user_id}", iamHandler.DeleteWorkspaceMember)

			// Agent路由
			agentHandler := v1.NewAgentHandler(h.platform.Agent, h.runtime, h.platform.Approvals)
			r.Post("/agents", agentHandler.CreateAgent)
			r.Get("/agents", agentHandler.ListAgents)
			r.Get("/agents/{agent_id}", agentHandler.GetAgent)
			r.Get("/agents/{agent_id}/versions", agentHandler.ListAgentVersions)
			r.Get("/agents/{agent_id}/input-schema", agentHandler.GetUserPromptInputSpec)
			r.Post("/agents/{agent_id}/versions", agentHandler.CreateAgentVersion)
			r.Post("/agents/{agent_id}/promote", agentHandler.PromoteAgentVersion)
			r.Post("/agents/{agent_id}/chat", agentHandler.Chat)
			r.Get("/conversations", agentHandler.ListConversations)
			r.Get("/conversations/{conversation_id}", agentHandler.GetConversation)

			// Tool Registry。
			toolHandler := v1.NewToolHandler(h.platform.Tool, h.platform.Registry)
			r.Post("/tools", toolHandler.CreateTool)
			r.Get("/tools", toolHandler.ListTools)
			r.Get("/tools/{tool_id}/versions", toolHandler.ListToolVersions)
			r.Post("/tools/{tool_id}/versions", toolHandler.CreateToolVersion)
			r.Post("/tools/{tool_id}/test", toolHandler.TestTool)
			r.Post("/tools/{tool_id}/publish", toolHandler.PublishTool)
			r.Post("/tools/{tool_id}/versions/{version_id}/publish", toolHandler.PublishToolVersion)

			// Knowledge Base。
			kbHandler := v1.NewKBHandler(h.platform.KB)
			r.Post("/kbs", kbHandler.CreateKB)
			r.Get("/kbs", kbHandler.ListKBs)
			r.Post("/kbs/{kb_id}/connectors/markdown/sync", kbHandler.SyncConnector)
			r.Get("/kbs/{kb_id}/connectors", kbHandler.ListConnectors)
			r.Get("/kbs/{kb_id}/documents", kbHandler.ListDocuments)
			r.Post("/kbs/{kb_id}/search", kbHandler.Search)
			r.Get("/kbs/{kb_id}/jobs", kbHandler.ListIngestJobs)

			// Prompt 版本库（发布统一由 AgentEnv 管理）。
			promptHandler := v1.NewPromptHandler(h.platform.Prompt)
			r.Post("/prompts", promptHandler.CreatePrompt)
			r.Get("/prompts", promptHandler.ListPrompts)
			r.Post("/prompts/{prompt_id}/versions", promptHandler.CreateVersion)
			r.Get("/prompts/{prompt_id}/versions", promptHandler.ListVersions)
			r.Get("/prompts/{prompt_id}/diff", promptHandler.Diff)
			r.Post("/prompts/{prompt_id}/render", promptHandler.Render)

			// 单层不可变模型配置；API Key 仅由部署环境注入。
			modelHandler := v1.NewModelConfigHandler(h.platform.ModelConfig)
			r.Get("/model-config-versions", modelHandler.ListConfigVersions)

			// Skills。
			skillHandler := v1.NewSkillHandler(h.platform.Skill, h.platform.Agent)
			r.Post("/skills", skillHandler.CreateSkill)
			r.Get("/skills", skillHandler.ListSkills)
			r.Get("/skills/{skill_id}/versions", skillHandler.ListVersions)
			r.Post("/skills/{skill_id}/versions", skillHandler.CreateVersion)
			r.Post("/skills/{skill_id}/publish", skillHandler.Publish)
			r.Post("/skills/{skill_id}/subscribe", skillHandler.Subscribe)

			// Audit：保留数据库内的运行审计。
			auditHandler := v1.NewAuditHandler(h.platform.Audit)
			r.Get("/audit/logs", auditHandler.Logs)

			// 人在环审批队列。
			approvalHandler := v1.NewApprovalHandler(h.platform.Approvals, h.jobs, h.platform.Audit)
			r.Get("/approvals", approvalHandler.List)
			r.Post("/approvals/{approval_id}/approve", approvalHandler.Approve)
			r.Post("/approvals/{approval_id}/reject", approvalHandler.Reject)

			// 用户信息
			r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
				userID := middleware.GetUserIDFromContext(r.Context())
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"user_id":     userID,
					"global_role": middleware.GetGlobalRoleFromContext(r.Context()),
				})
			})

			// 向 Admin 暴露可观测端点配置。
			r.Get("/observability", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"metrics_url":         "/metrics",
					"healthz_url":         "/healthz",
					"readyz_url":          "/readyz",
					"otlp_endpoint":       h.observability.OTLPEndpoint,
					"traces_enabled":      h.observability.OTLPEndpoint != "",
					"langfuse_ui_url":     h.observability.LangfuseUIURL,
					"langfuse_project_id": h.observability.LangfuseProjectID,
				})
			})
		})
	})

	// 流式API路由
	r.Route("/stream", func(r chi.Router) {
		r.Use(middleware.Auth(h.platform.IAM))
		r.Use(middleware.Workspace(h.platform.IAM))

		// SSE流式聊天
		sseHandler := streaming.NewSSEHandler(h.runtime)
		r.Post("/agents/{agent_id}/chat", sseHandler.ChatStream)

	})

	// 静态文件服务（Admin UI）：只提供当前 React 构建产物。
	r.Handle("/*", spaFileServer(resolveWebRoot()))

	return r
}
