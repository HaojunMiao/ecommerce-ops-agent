package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"

	courseotel "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/otel"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/audit"
	platformskill "github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/skill"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/skillrunner"
)

// ChatRequest 是一次运行请求；ConversationID 用于多轮会话续接。
type ChatRequest struct {
	ConversationID   string `json:"conversation_id"`
	Message          string `json:"message"`
	WorkspaceID      string `json:"-"`
	UserID           string `json:"-"`
	AgentEnvironment string `json:"agent_env,omitempty"`
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

func (e *Engine) ChatStream(ctx context.Context, req ChatRequest, emit Emitter) (runErr error) {
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

	// 整轮运行作为根 Span；后续模型与工具 Span 都从这个 ctx 派生。
	ctx, finishTrace := courseotel.StartRun(ctx, courseotel.RunContext{
		WorkspaceID: snapshot.WorkspaceID, AgentVersionID: snapshot.ID,
		ConversationID: req.ConversationID, UserID: req.UserID,
	})
	defer func() { finishTrace(runErr) }()
	runStatus := "failed"
	defer func() {
		if e.audit == nil || req.UserID == "" || snapshot.WorkspaceID == "" {
			return
		}
		// 客户端断开不应抹掉已经发生的审计事实，因此移除原请求的取消信号。
		_, _ = e.audit.Append(context.WithoutCancel(ctx), audit.Event{
			WorkspaceID: snapshot.WorkspaceID, ActorID: req.UserID,
			Action: "agent.run." + runStatus, ResourceID: req.ConversationID,
			Data: map[string]any{"agent_version_id": snapshot.ID},
		})
	}()

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
	// 输入 Guard 必须发生在保存历史和调用模型之前；PII 脱敏后，数据库和模型
	// 都只会看到 SanitizedText，注入或超长输入则直接结束本次运行。
	input := req.Message
	if e.guard != nil {
		decision, guardErr := e.guard.Evaluate(ctx, snapshot.WorkspaceID, "on_input", input)
		if guardErr != nil {
			return fmt.Errorf("evaluate input guard: %w", guardErr)
		}
		if err := emitContext(ctx, emit, Event{Type: "guard_decision", Data: decision}); err != nil {
			return err
		}
		if !decision.Allowed {
			if err := emitContext(ctx, emit, Event{Type: "guard_blocked", Data: map[string]any{
				"hook": "on_input", "reasons": decision.Reasons,
			}}); err != nil {
				return err
			}
			runStatus = "blocked"
			return emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": runStatus}})
		}
		input = decision.SanitizedText
		plan = e.guardExecutionPlan(plan, snapshot.WorkspaceID)
	}
	// observe 放在 Guard 包装之后，形成 observe → guard → 真实模型的调用链。
	plan = observeExecutionPlan(plan)
	// 每轮模型输入都按“系统提示词 -> 历史消息 -> 本轮用户消息”重新组装。
	// 如果控制面实现了 ConversationMessageStore，就先恢复历史，再保存本轮用户输入。
	messages := []*schema.Message{schema.SystemMessage(systemPrompt)}
	if history, ok := e.platform.(ConversationMessageStore); ok {
		stored, historyErr := history.ListMessages(ctx, snapshot.WorkspaceID, req.ConversationID)
		if historyErr != nil {
			return fmt.Errorf("load conversation history: %w", historyErr)
		}
		for _, message := range stored {
			switch message.Role {
			case "user":
				messages = append(messages, schema.UserMessage(message.Content))
			case "assistant":
				messages = append(messages, schema.AssistantMessage(message.Content, nil))
			}
		}
		if historyErr := history.AppendMessage(ctx, snapshot.WorkspaceID, req.ConversationID, "user", input); historyErr != nil {
			return fmt.Errorf("persist user message: %w", historyErr)
		}
	}
	messages = append(messages, schema.UserMessage(input))
	answer, streamed, err := e.runPlan(ctx, req.ConversationID, req.UserID, snapshot, plan, packages, messages, emit)
	if err != nil {
		var awaiting *AwaitingApprovalError
		if errors.As(err, &awaiting) {
			if emitErr := emitContext(ctx, emit, Event{Type: "approval_requested", Data: map[string]string{
				"approval_id": awaiting.ApprovalID, "tool_name": awaiting.ToolName,
				"tool_call_id": awaiting.ToolCallID, "tool_version_id": awaiting.ToolVersionID,
			}}); emitErr != nil {
				return emitErr
			}
			runStatus = "awaiting_approval"
			return emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": runStatus}})
		}
		_ = emitContext(ctx, emit, Event{Type: "error", Data: map[string]string{"message": err.Error()}})
		return fmt.Errorf("generate: %w", err)
	}
	// 输出在持久化和发送客户端前再过一次 Guard，避免模型回显个人信息。
	if e.guard != nil {
		decision, guardErr := e.guard.Evaluate(ctx, snapshot.WorkspaceID, "on_output", answer.Content)
		if guardErr != nil {
			return fmt.Errorf("evaluate output guard: %w", guardErr)
		}
		if !decision.Allowed {
			if err := emitContext(ctx, emit, Event{Type: "guard_blocked", Data: map[string]any{
				"hook": "on_output", "reasons": decision.Reasons,
			}}); err != nil {
				return err
			}
			runStatus = "blocked"
			return emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": runStatus}})
		}
		answer.Content = decision.SanitizedText
	}
	if history, ok := e.platform.(ConversationMessageStore); ok {
		if historyErr := history.AppendMessage(ctx, snapshot.WorkspaceID, req.ConversationID, "assistant", answer.Content); historyErr != nil {
			return fmt.Errorf("persist assistant message: %w", historyErr)
		}
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
	if err := emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": "completed"}}); err != nil {
		return err
	}
	runStatus = "completed"
	return nil
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
	ctx context.Context, conversationID, actorID string, snapshot *AgentSnapshot, plan *llm.ExecutionPlan,
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
			if e.audit != nil && actorID != "" {
				_, _ = e.audit.Append(context.WithoutCancel(ctx), audit.Event{
					WorkspaceID: snapshot.WorkspaceID, ActorID: actorID, Action: "skill.triggered",
					ResourceID: pkg.Name, Data: map[string]any{"conversation_id": conversationID},
				})
			}
			return emitContext(ctx, emit, Event{Type: "skill_trigger", Data: map[string]string{"name": pkg.Name}})
		},
	)
	if err != nil {
		return nil, false, err
	}
	if skillRuntime != nil && skillRuntime.ExplicitName != "" {
		messages[0].Content += fmt.Sprintf("\n\n用户已显式选择 Skill %q；请先调用 skill 工具加载完整说明。", skillRuntime.ExplicitName)
	}
	if len(bindings) > 0 || plan.Retry != nil || plan.Failover != nil || skillRuntime != nil || e.guard != nil {
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
			WithToolAuthorization(authorize).
			WithApprovals(e.approvals, conversationID).
			WithAudit(e.audit, actorID)
		if skillRuntime != nil {
			runner.WithHandlers(skillRuntime.Handlers...).
				WithSkillState(skillRuntime.ActiveName, skillRuntime.Restore)
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

// guardedChatModel 装饰每一个实际模型部署，在每次 Generate/Stream 前消费一次配额。
// ReAct 一轮可能多次调用模型，备用模型也会被包装，因此计数不是“一次 HTTP 对话一次”。
type guardedChatModel struct {
	next        model.BaseChatModel
	guard       RuntimeGuard
	workspaceID string
}

func (m guardedChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if err := m.beforeCall(ctx); err != nil {
		return nil, err
	}
	return m.next.Generate(ctx, messages, opts...)
}

func (m guardedChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if err := m.beforeCall(ctx); err != nil {
		return nil, err
	}
	return m.next.Stream(ctx, messages, opts...)
}

func (m guardedChatModel) beforeCall(ctx context.Context) error {
	decision, err := m.guard.Evaluate(ctx, m.workspaceID, "on_llm_call", "")
	if err != nil {
		return fmt.Errorf("evaluate LLM quota: %w", err)
	}
	if !decision.Allowed {
		return fmt.Errorf("LLM call blocked by guard: %s", strings.Join(decision.Reasons, ","))
	}
	return nil
}

// guardExecutionPlan 同时包装主模型和故障切换得到的备用模型，避免切换模型绕过配额。
func (e *Engine) guardExecutionPlan(plan *llm.ExecutionPlan, workspaceID string) *llm.ExecutionPlan {
	if e.guard == nil || plan == nil {
		return plan
	}
	guarded := *plan
	guarded.Model = guardedChatModel{next: plan.Model, guard: e.guard, workspaceID: workspaceID}
	if plan.Failover != nil {
		failover := *plan.Failover
		original := plan.Failover.GetFailoverModel
		failover.GetFailoverModel = func(
			ctx context.Context, state *adk.FailoverContext[*schema.Message],
		) (model.BaseChatModel, []*schema.Message, error) {
			next, replacement, err := original(ctx, state)
			if err != nil {
				return nil, replacement, err
			}
			return guardedChatModel{next: next, guard: e.guard, workspaceID: workspaceID}, replacement, nil
		}
		guarded.Failover = &failover
	}
	return &guarded
}

// observedChatModel 为每次模型调用创建子 Span；主模型与故障切换模型都会被包装。
type observedChatModel struct{ next model.BaseChatModel }

func (m observedChatModel) Generate(
	ctx context.Context, messages []*schema.Message, opts ...model.Option,
) (answer *schema.Message, err error) {
	ctx, finish := courseotel.StartOperation(ctx, "llm.generate",
		attribute.String("gen_ai.operation.name", "chat"),
	)
	defer func() { finish(err) }()
	return m.next.Generate(ctx, messages, opts...)
}

func (m observedChatModel) Stream(
	ctx context.Context, messages []*schema.Message, opts ...model.Option,
) (stream *schema.StreamReader[*schema.Message], err error) {
	ctx, finish := courseotel.StartOperation(ctx, "llm.stream",
		attribute.String("gen_ai.operation.name", "chat"),
	)
	defer func() { finish(err) }()
	return m.next.Stream(ctx, messages, opts...)
}

func observeExecutionPlan(plan *llm.ExecutionPlan) *llm.ExecutionPlan {
	if plan == nil {
		return nil
	}
	observed := *plan
	observed.Model = observedChatModel{next: plan.Model}
	if plan.Failover != nil {
		failover := *plan.Failover
		original := plan.Failover.GetFailoverModel
		failover.GetFailoverModel = func(
			ctx context.Context, state *adk.FailoverContext[*schema.Message],
		) (model.BaseChatModel, []*schema.Message, error) {
			next, replacement, err := original(ctx, state)
			if err != nil {
				return nil, replacement, err
			}
			return observedChatModel{next: next}, replacement, nil
		}
		observed.Failover = &failover
	}
	return &observed
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
