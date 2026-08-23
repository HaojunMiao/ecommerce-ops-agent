package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

// 带优雅退出的服务
func main() {

	// 创建一个标准库路由器。mux->multiplexer，HTTP请求多路复用器
	mux := http.NewServeMux()

	// 注册健康检查接口
	// 收到/healthz请求时，执行该匿名函数，返回200状态码和ok字符串
	// _ *http.Request表示不需要读取请求体内容
	// w http.ResponseWriter用于写HTTP响应
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok")) // 把字符串ok转成字节写入响应体，忽略返回的字节数和错误
	})

	// 创建HTTP server，监听8080端口
	// 收到请求后交给mux做路由分发
	// 客户端必须在五秒内发送完 HTTP Header，否则服务端终止读取。这可以避免客户端长时间占用连接
	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
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
	log.Println("server listening on :8080")
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
