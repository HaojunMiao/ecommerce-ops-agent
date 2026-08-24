package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/engine"
)

// ChatRuntime 让 HTTP 层只感知事件流，不依赖 Engine 的内部步骤。
// 即HTTP对runtime的最小要求是能执行ChatStream()就可以。
type ChatRuntime interface {
	ChatStream(ctx context.Context, req engine.ChatRequest, emit engine.Emitter) error
}

// StreamHandler 的 SSE 编码与取消传播
type StreamHandler struct {
	runtime ChatRuntime
}

func NewStreamHandler(runtime ChatRuntime) *StreamHandler {
	return &StreamHandler{
		runtime: runtime,
	}
}

// 给客户端响应流式数据
// 把runtime的event转成SSE协议
// SSE 是一种保持 HTTP 响应连接、让服务端不断向客户端推送文本事件的协议。
/*
每条消息格式大致是：
data: {"type":"answer_delta","text":"你"}

data: {"type":"answer_delta","text":"好"}

关键是每条消息后有两个换行：\n\n（表示这一帧 SSE 事件结束。）
*/

// 作为HTTP Handler。接收请求、解析为ChatRequest、调用runtime.ChatStream、接收Event编码成SSE、写给客户端
func (h *StreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		http.Error(w, "chat runtime is unavailable", http.StatusServiceUnavailable)
		return
	}
	// 接收请求
	var req engine.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream") // 响应头标识流式
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// 发起流式调用
	errorEventWritten := false
	// 传入的这个匿名函数就是Emitter，表示将Event转JSON、写入HTTP响应、Flush，使客户端能立即收到
	// 即执行emit(event)就是执行这个匿名函数
	err := h.runtime.ChatStream(r.Context(), req, func(event engine.Event) error {
		// 把后端调用大模型返回的流式输出，给客户端返回
		if event.Type == "error" {
			errorEventWritten = true
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}

		if _, err = fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush() // 写多少返回多少
		// Flush() 的作用是强制把服务端缓冲区中的内容立即发送出去，以保证流式效果
		return nil

	})
	if err != nil && !errorsIsCanceled(err) && !errorEventWritten {
		payload, _ := json.Marshal(engine.Event{
			Type: "error",
			Data: map[string]string{
				"message": err.Error(),
			},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
}

func errorsIsCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
