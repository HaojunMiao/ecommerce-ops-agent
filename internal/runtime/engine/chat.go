package engine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// ChatRequest 是一次运行请求；ConversationID 用于多轮会话续接。
type ChatRequest struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
	WorkspaceID    string `json:"-"`
}

// Event 是 Runtime 对传输层公开的稳定事件信封。
// 为什么不直接发送字符串？因为除了模型文字，还需要表达运行状态
// run_started表示本次运行开始、answer_delta表示每次流式返回一小块文字...等
// 前端拿到不同的Type，可以做不同的处理。
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	Text string `json:"text,omitempty"`
}

// Emitter是一种函数，接收Event，返回error
type Emitter func(Event) error

// client --> server --> LLM
// 需要某种机制使得client端能及时接收回答
// server不能完全接收完才返回给client，而是应该收到多少就返回多少
// Emit Event就是干这个的
// Eino Stream()产生chunke（从大模型逐块读取内容），emit(Event)把runtime得到的内容交给上层客户端
// Eino Stream产生chunk、Engine把chunke包装成Event、emit把Event交给HTTP层、SSE写入并Flush、客户端才能看到增量内容

func (e *Engine) ChatStream(ctx context.Context, req ChatRequest, emit Emitter) error {
	if strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.Message) == "" {
		return fmt.Errorf("conversation_id and message are required")
	}
	if emit == nil {
		return fmt.Errorf("emitter is required")
	}
	// 根据输入的会话id去加载会话信息
	// 先找到会话，再找到该会话固定使用的 AgentSnapshot
	snapshot, err := e.ResolveSnapshot(ctx, req.ConversationID)
	if err != nil {
		return err
	}
	if req.WorkspaceID != "" && snapshot.WorkspaceID != "" && req.WorkspaceID != snapshot.WorkspaceID {
		return fmt.Errorf("conversation is outside the active workspace")
	}

	if err := emitContext(ctx, emit, Event{
		Type: "run_started",
		Data: map[string]string{
			"conversation_id":  req.ConversationID,
			"agent_version_id": snapshot.ID,
		},
	}); err != nil {
		return err
	}

	// 执行会话：没有绑定工具时直接流式调用模型；绑定工具时交给 Eino ADK 执行 ReAct。
	messages := []*schema.Message{
		schema.SystemMessage(snapshot.SystemPrompt), // 每一个agent有一个系统提示词
		schema.UserMessage(req.Message),
	}
	var answer *schema.Message
	streamed := false
	if len(snapshot.ToolVersionIDs) > 0 {
		if e.tools == nil {
			return fmt.Errorf("tool runtime is required by the pinned agent snapshot")
		}
		bindings, bindErr := e.tools.Bind(ctx, snapshot.WorkspaceID, snapshot.ToolVersionIDs)
		if bindErr != nil {
			return fmt.Errorf("bind pinned tools: %w", bindErr)
		}
		answer, err = NewADKRunner(e.model, e.tools, snapshot.WorkspaceID).Run(ctx, messages, bindings, snapshot.MaxSteps, emit)
	} else {
		if e.model == nil {
			return fmt.Errorf("chat model is required")
		}
		stream, streamErr := e.model.Stream(ctx, messages)
		if streamErr != nil {
			err = streamErr
		} else {
			defer stream.Close()
			var chunks []*schema.Message
			for {
				chunk, recvErr := stream.Recv()
				if recvErr == io.EOF {
					break
				}
				if recvErr != nil {
					err = recvErr
					break
				}
				chunks = append(chunks, chunk)
				if chunk != nil && chunk.Content != "" {
					if emitErr := emitContext(ctx, emit, Event{Type: "answer_delta", Text: chunk.Content}); emitErr != nil {
						err = emitErr
						break
					}
					streamed = true
				}
			}
			if err == nil {
				answer, err = schema.ConcatMessages(chunks)
			}
		}
	}
	if err != nil {
		_ = emitContext(ctx, emit, Event{Type: "error", Data: map[string]string{"message": err.Error()}})
		return fmt.Errorf("generate: %w", err)
	}
	if !streamed {
		for _, delta := range answerDeltas(answer.Content, 8) {
			if err := emitContext(ctx, emit, Event{Type: "answer_delta", Text: delta}); err != nil {
				return err
			}
		}
	}
	if err := emitContext(ctx, emit, Event{Type: "answer_done", Text: answer.Content}); err != nil {
		return err
	}
	return emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": "completed"}})
}

func answerDeltas(answer string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = 8
	}
	runes := []rune(answer)
	if len(runes) == 0 {
		return nil
	}
	deltas := make([]string, 0, (len(runes)+maxRunes-1)/maxRunes)
	for start := 0; start < len(runes); start += maxRunes {
		end := min(start+maxRunes, len(runes))
		deltas = append(deltas, string(runes[start:end]))
	}
	return deltas
}

// 给emit包了一层”取消检查“
// 如果ctx.Done()关闭（如请求超时、客户端主动取消请求、用户关闭页面->即请求断开了），则不再向客户端发送，即return Err
// 否则，正常发送：return emit(Event)
func emitContext(ctx context.Context, emit Emitter, event Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return emit(event)
	}
}
