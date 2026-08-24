package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/iam"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
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
	if _, err := llm.NewGateway(cfg); err != nil {
		log.Fatalf("create LLM Gateway failed, err:%v", err)
	}

	// 创建HTTP server，监听8080端口
	// 收到请求后交给mux做路由分发
	// 客户端必须在五秒内发送完 HTTP Header，否则服务端终止读取。这可以避免客户端长时间占用连接
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(iamService),
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
