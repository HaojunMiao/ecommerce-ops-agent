package engine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"

	platformskill "github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/skill"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/skillrunner"
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

	// 先按快照固定的模型配置版本准备主模型、重试和故障切换策略。
	plan, err := e.executionPlan(ctx, snapshot)
	if err != nil {
		return err
	}
	// 兼容旧快照的字面量 SystemPrompt；新快照优先按 PromptVersionID 解析不可变版本。
	systemPrompt := snapshot.SystemPrompt
	if snapshot.PromptVersionID != "" {
		if e.prompts == nil {
			return fmt.Errorf("prompt resolver is required by the pinned snapshot")
		}
		systemPrompt, err = e.prompts.Render(ctx, snapshot.WorkspaceID, snapshot.PromptVersionID, map[string]string{})
		if err != nil {
			return fmt.Errorf("render pinned system prompt: %w", err)
		}
	}
	// 仅解析快照固定的 Skill 版本，不会跟随后续发布自动漂移。
	packages, err := e.resolveSkills(ctx, snapshot)
	if err != nil {
		return err
	}
	if err := emitContext(ctx, emit, Event{Type: "run_started", Data: map[string]string{
		"conversation_id": req.ConversationID, "agent_version_id": snapshot.ID,
	}}); err != nil {
		return err
	}
	messages := []*schema.Message{schema.SystemMessage(systemPrompt), schema.UserMessage(req.Message)}
	answer, streamed, err := e.runPlan(ctx, snapshot, plan, packages, messages, emit)
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

func (e *Engine) resolveSkills(ctx context.Context, snapshot *AgentSnapshot) ([]platformskill.Package, error) {
	if len(snapshot.SkillVersionIDs) == 0 {
		return nil, nil
	}
	if e.skills == nil {
		return nil, fmt.Errorf("skill resolver is required by the pinned snapshot")
	}
	packages := make([]platformskill.Package, 0, len(snapshot.SkillVersionIDs))
	for _, versionID := range snapshot.SkillVersionIDs {
		version, err := e.skills.Resolve(ctx, snapshot.WorkspaceID, versionID)
		if err != nil {
			return nil, fmt.Errorf("resolve pinned skill %s: %w", versionID, err)
		}
		packages = append(packages, version.Package)
	}
	return packages, nil
}

// executionPlan 优先解析快照中固定的模型配置版本；旧快照没有版本 ID 时回退到启动时全局模型。
func (e *Engine) executionPlan(ctx context.Context, snapshot *AgentSnapshot) (*llm.ExecutionPlan, error) {
	if snapshot.ModelProfileVersionID == "" {
		if e.model == nil {
			return nil, fmt.Errorf("chat model is required")
		}
		return &llm.ExecutionPlan{Model: e.model}, nil
	}
	if e.profiles == nil || e.planner == nil {
		return nil, fmt.Errorf("model profile resolver and execution planner are required by the pinned snapshot")
	}
	profile, err := e.profiles.Resolve(ctx, snapshot.WorkspaceID, snapshot.ModelProfileVersionID)
	if err != nil {
		return nil, fmt.Errorf("resolve pinned model profile: %w", err)
	}
	plan, err := e.planner.PrepareExecution(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("prepare model execution: %w", err)
	}
	return plan, nil
}

// runPlan 统一执行工具绑定和模型策略：有工具、重试或备用模型时交给 ADK，
// 否则保留直接 Stream 的轻量路径。
func (e *Engine) runPlan(
	ctx context.Context, snapshot *AgentSnapshot, plan *llm.ExecutionPlan,
	packages []platformskill.Package, messages []*schema.Message, emit Emitter,
) (*schema.Message, bool, error) {
	if plan == nil || plan.Model == nil {
		return nil, false, fmt.Errorf("execution plan model is required")
	}
	var bindings []ToolBinding
	if len(snapshot.ToolVersionIDs) > 0 {
		if e.tools == nil {
			return nil, false, fmt.Errorf("tool runtime is required by the pinned agent snapshot")
		}
		var err error
		bindings, err = e.tools.Bind(ctx, snapshot.WorkspaceID, snapshot.ToolVersionIDs)
		if err != nil {
			return nil, false, fmt.Errorf("bind pinned tools: %w", err)
		}
	}
	// Skill Runtime 依赖 Agent 已绑定的工具，用于检查 AllowedTools 和构造执行时权限策略。
	skillRuntime, err := skillrunner.NewRuntime(
		ctx, packages, bindings, messages[len(messages)-1].Content,
		func(pkg platformskill.Package) error {
			return emitContext(ctx, emit, Event{Type: "skill_trigger", Data: map[string]string{"name": pkg.Name}})
		},
	)
	if err != nil {
		return nil, false, err
	}
	if skillRuntime != nil && skillRuntime.ExplicitName != "" {
		messages[0].Content += fmt.Sprintf("\n\n用户已显式选择 Skill %q；请先调用 skill 工具加载完整说明。", skillRuntime.ExplicitName)
	}
	if len(bindings) > 0 || plan.Retry != nil || plan.Failover != nil || skillRuntime != nil {
		maxSteps := snapshot.MaxSteps
		if maxSteps <= 0 {
			maxSteps = 4
		}
		var authorize func(string, string) error
		if skillRuntime != nil {
			authorize = skillRuntime.Authorize
		}
		runner := NewADKRunner(plan.Model, e.tools, snapshot.WorkspaceID).
			WithModelPolicy(plan.Retry, plan.Failover).
			WithToolAuthorization(authorize)
		if skillRuntime != nil {
			runner.WithHandlers(skillRuntime.Handlers...)
		}
		answer, err := runner.Run(ctx, messages, bindings, maxSteps, emit)
		return answer, false, err
	}

	stream, err := plan.Model.Stream(ctx, messages)
	if err != nil {
		return nil, false, err
	}
	defer stream.Close()
	var chunks []*schema.Message
	streamed := false
	for {
		chunk, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return nil, streamed, recvErr
		}
		chunks = append(chunks, chunk)
		if chunk != nil && chunk.Content != "" {
			if err := emitContext(ctx, emit, Event{Type: "answer_delta", Text: chunk.Content}); err != nil {
				return nil, streamed, err
			}
			streamed = true
		}
	}
	answer, err := schema.ConcatMessages(chunks)
	return answer, streamed, err
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
