package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/iam"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/kb"
	platformtool "github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/engine"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/sandbox"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/tooling"
)

// 带优雅退出的服务
func main() {
	// 加载配置
	cfg := config.Load()
	// 校验是否能正常加载配置
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	iamService := iam.New(iam.NewMemoryStore(), cfg.JWTSecret, cfg.JWTIssuer)

	// 初始化LLM
	gateway, err := llm.NewGateway(cfg)
	if err != nil {
		log.Fatalf("create LLM Gateway failed, err:%v", err)
	}

	// platform
	controlPlane := platform.New()
	controlPlane.PutConversation(&domain.Conversation{
		ID:             "demo-conversation",
		AgentID:        "demo",
		AgentVersionID: "demo-v1",
	})
	controlPlane.PutSnapshot(&engine.AgentSnapshot{
		ID:           "demo-v1",
		AgentID:      "demo",
		SystemPrompt: "你是一个 eino 框架学习助手",
		MaxSteps:     4,
	})
	runtime := engine.New(controlPlane, gateway)
	toolRegistry := platformtool.NewRegistry()
	// 第 10 课使用进程内存保存知识库和文档，服务重启后数据会丢失。
	knowledgeBases := kb.NewService()
	// 检索服务通过 DocumentSource 接口读取 knowledgeBases 中已经切好的文档。
	knowledgeSearch := retriever.NewKnowledgeSearch(knowledgeBases)
	sandboxClient, err := sandbox.NewClient(cfg.SandboxRunnerURL, cfg.SandboxRunnerToken)
	if err != nil {
		log.Fatalf("create sandbox runner client: %v", err)
	}
	toolExecutor := tooling.NewExecutor(toolRegistry, nil, "crossborder-sim", "localhost", "127.0.0.1").WithSandbox(sandboxClient)
	// 将知识检索注册为进程内工具：模型调用时不发 HTTP，而是直接执行 Go 函数。
	toolExecutor.RegisterSDK("search_knowledge_base", func(
		ctx context.Context, workspaceID string, arguments map[string]any,
	) (tooling.Result, error) {
		kbID, _ := arguments["kb_id"].(string)
		query, _ := arguments["query"].(string)
		mode, _ := arguments["mode"].(string)
		topK := 5
		// Executor 使用 decoder.UseNumber()，因此 JSON 数字在 map 中是 json.Number。
		if number, ok := arguments["top_k"].(json.Number); ok {
			if value, parseErr := number.Int64(); parseErr == nil {
				topK = int(value)
			}
		}
		results, searchErr := knowledgeSearch.Search(ctx, workspaceID, kbID, query, mode, topK)
		if searchErr != nil {
			return tooling.Result{}, searchErr
		}
		body, marshalErr := json.Marshal(results)
		return tooling.Result{StatusCode: http.StatusOK, Body: body}, marshalErr
	})
	runtime.WithTools(toolExecutor)

	// 创建HTTP server，监听8080端口
	// 客户端必须在五秒内发送完 HTTP Header，否则服务端终止读取。这可以避免客户端长时间占用连接
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.NewRouterWithControlPlane(iamService, runtime, api.ControlPlane{
			Tools:  toolRegistry,
			KBs:    knowledgeBases,
			Search: knowledgeSearch,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 优雅退出逻辑
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_ = server.Shutdown(shutdownCtx)
	}()

	// 启动服务
	log.Printf("server listening on %s", cfg.HTTPAddr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
