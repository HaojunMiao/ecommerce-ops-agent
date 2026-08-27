package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/tooling"
)

const frameworkCheckpointVersion = 1

// 以下结构会进入 Eino 的检查点，因此必须注册稳定名称；后续重构 Go 类型名时，
// 已持久化的检查点仍能被框架反序列化。
type approvalInterruptInfo struct {
	ApprovalID, RunID, ToolName, ToolCallID, ToolVersionID, Arguments, ActiveSkillName string
}

type approvalInterruptState struct{ Info approvalInterruptInfo }
type approvalResumeDecision struct{ Approved bool }

// persistedFrameworkCheckpoint 同时保存 Eino 原始检查点和具体中断 ID，
// 恢复时必须把人工决策送回同一个中断点。
type persistedFrameworkCheckpoint struct {
	Version         int    `json:"version"`
	InterruptID     string `json:"interrupt_id"`
	ActiveSkillName string `json:"active_skill_name,omitempty"`
	Data            []byte `json:"data"`
}

func init() {
	schema.RegisterName[approvalInterruptInfo]("course_approval_interrupt_info_v1")
	schema.RegisterName[approvalInterruptState]("course_approval_interrupt_state_v1")
	schema.RegisterName[approvalResumeDecision]("course_approval_resume_decision_v1")
}

// ToolBinding 直接复用 tooling.Binding。
// 它描述的是“本次 Agent 运行允许使用的某个固定版本工具”，其中 Info 会提供给大模型，
// VersionID 则在真正执行时用于从平台注册表中找到那个不可变版本。
type ToolBinding = tooling.Binding

// ToolExecutor 是 ReAct 层对工具执行器的最小依赖。
// react.go 只要求它能够执行一次工具调用，不关心底层调用的是 REST 服务还是沙箱。
type ToolExecutor interface {
	Execute(ctx context.Context, call tooling.Call) (tooling.Result, error)
}

// ApprovalGate 是运行时创建审批单和保存框架检查点所需的最小接口。
type ApprovalGate interface {
	Create(ctx context.Context, request approval.Request) (*approval.Request, error)
	SaveCheckpoint(ctx context.Context, workspaceID, requestID string, checkpoint []byte) error
}

// ADKRunner 负责把本项目的平台对象装配成 Eino ADK 能运行的 Agent。
// model 负责推理和决定是否调用工具；executor 负责真正执行工具；
// workspaceID 用于保证工具只能在当前 Workspace 范围内解析和调用。
type ADKRunner struct {
	model        model.BaseChatModel
	executor     ToolExecutor
	workspaceID  string
	retry        *adk.ModelRetryConfig
	failover     *adk.ModelFailoverConfig[*schema.Message]
	handlers     []adk.ChatModelAgentMiddleware
	authorize    func(name, arguments string) error
	approvals    ApprovalGate
	runID        string
	activeSkill  func() string
	restoreSkill func(string) error
}

func NewADKRunner(chatModel model.BaseChatModel, executor ToolExecutor, workspaceID string) *ADKRunner {
	return &ADKRunner{model: chatModel, executor: executor, workspaceID: workspaceID}
}

// WithModelPolicy 把模型配置版本解析出的重试与故障切换策略交给 Eino ADK。
func (r *ADKRunner) WithModelPolicy(
	retry *adk.ModelRetryConfig, failover *adk.ModelFailoverConfig[*schema.Message],
) *ADKRunner {
	r.retry, r.failover = retry, failover
	return r
}

// WithHandlers 注入 Eino Skill 等 Agent Middleware。
func (r *ADKRunner) WithHandlers(handlers ...adk.ChatModelAgentMiddleware) *ADKRunner {
	r.handlers = append(r.handlers, handlers...)
	return r
}

// WithToolAuthorization 注入工具执行前的 Skill 权限校验。
func (r *ADKRunner) WithToolAuthorization(authorize func(name, arguments string) error) *ADKRunner {
	r.authorize = authorize
	return r
}

func (r *ADKRunner) WithApprovals(approvals ApprovalGate, runID string) *ADKRunner {
	r.approvals, r.runID = approvals, runID
	return r
}

// WithSkillState 告诉运行器如何保存和恢复当前 Skill 的激活状态。
func (r *ADKRunner) WithSkillState(active func() string, restore func(string) error) *ADKRunner {
	r.activeSkill, r.restoreSkill = active, restore
	return r
}

// AwaitingApprovalError 不是运行失败，而是通知上层本次运行已安全暂停。
type AwaitingApprovalError struct {
	ApprovalID, ToolName, ToolCallID, ToolVersionID string
}

func (e *AwaitingApprovalError) Error() string {
	return "tool call is awaiting approval: " + e.ApprovalID
}

// 执行一次完整的、支持工具调用的 ReAct Agent 运行，最后返回模型的最终回答。
// messages：发给 Agent 的初始消息，目前通常是系统提示词和用户消息。
// bindings：这个 Agent 版本允许使用的工具。
// maxSteps：ReAct 最大迭代次数，防止 Agent 无限调用工具。
// emit：向 SSE 层发送 tool_started、tool_finished 事件。
// 返回值：模型最后给用户的回答。
func (r *ADKRunner) Run(
	ctx context.Context, messages []*schema.Message, bindings []ToolBinding, maxSteps int, emit Emitter,
) (*schema.Message, error) {
	agent, err := r.newAgent(ctx, bindings, maxSteps, emit)
	if err != nil {
		return nil, err
	}
	// Eino 在 StatefulInterrupt 时把完整运行状态写入 CheckPointStore。
	// 课堂实现先保存在当前进程内，收到中断事件后再持久化到审批记录。
	store := &frameworkCheckpointStore{}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: store})
	iterator := runner.Run(ctx, messages, adk.WithCheckPointID(r.runID))
	answer, interrupted, err := consumeADKEvents(iterator)
	if err != nil {
		return nil, err
	}
	if len(interrupted) > 0 {
		return nil, r.persistInterrupts(ctx, store.Latest(), interrupted)
	}
	if answer == nil {
		return nil, fmt.Errorf("Eino ADK run finished without a final answer")
	}
	return answer, nil
}

// Resume 从已有检查点恢复 Eino Runner，并把 Approved=true 只投递给指定中断。
// 因此它不是“重新执行一次 Run”，已完成的模型推理和工具步骤不会从头再来。
func (r *ADKRunner) Resume(
	ctx context.Context, checkpointID string, checkpoint []byte, interruptID string,
	bindings []ToolBinding, maxSteps int, emit Emitter,
) (*schema.Message, error) {
	agent, err := r.newAgent(ctx, bindings, maxSteps, emit)
	if err != nil {
		return nil, err
	}
	store := newFrameworkCheckpointStore(checkpointID, checkpoint)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: store})
	iterator, err := runner.ResumeWithParams(ctx, checkpointID, &adk.ResumeParams{Targets: map[string]any{
		interruptID: approvalResumeDecision{Approved: true},
	}})
	if err != nil {
		return nil, err
	}
	answer, interrupted, err := consumeADKEvents(iterator)
	if err != nil {
		return nil, err
	}
	// 续跑后模型仍可能请求另一个敏感工具，此时保存新的中断继续等待审批。
	if len(interrupted) > 0 {
		return nil, r.persistInterrupts(ctx, store.Latest(), interrupted)
	}
	if answer == nil {
		return nil, fmt.Errorf("Eino ADK resume finished without a final answer")
	}
	return answer, nil
}

func (r *ADKRunner) newAgent(
	ctx context.Context, bindings []ToolBinding, maxSteps int, emit Emitter,
) (*adk.ChatModelAgent, error) {
	if r.model == nil || (len(bindings) > 0 && r.executor == nil) {
		return nil, fmt.Errorf("chat model and required tool executor are required")
	}
	if maxSteps <= 0 {
		return nil, fmt.Errorf("max steps must be positive")
	}
	tools := make([]einotool.BaseTool, 0, len(bindings))
	byName := make(map[string]ToolBinding, len(bindings))
	for _, binding := range bindings {
		byName[binding.Name] = binding
		tools = append(tools, &bindingTool{binding: binding, executor: r.executor, workspaceID: r.workspaceID})
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "course_agent",
		Description: "kbot course agent",
		Model:       r.model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
				// 顺序执行工具，避免并行工具调用产生难以控制的副作用和顺序问题。
				ExecuteSequentially: true,
				ToolCallMiddlewares: []compose.ToolMiddleware{
					toolEventMiddleware(emit, r.authorize, r.approvalInterrupt(byName)),
				},
			}},
		MaxIterations:       maxSteps,
		Handlers:            r.handlers,
		ModelRetryConfig:    r.retry,
		ModelFailoverConfig: r.failover,
	})
}

// approvalInterrupt 在敏感工具真正执行前创建审批单并触发有状态中断。
// 普通工具直接放行；恢复阶段只有收到对当前中断的批准决策才会继续。
func (r *ADKRunner) approvalInterrupt(bindings map[string]ToolBinding) func(context.Context, *compose.ToolInput) error {
	return func(ctx context.Context, input *compose.ToolInput) error {
		binding, ok := bindings[input.Name]
		if !ok || !binding.Sensitive {
			return nil
		}
		if r.approvals == nil {
			return fmt.Errorf("sensitive tool %s requires an approval service", binding.Name)
		}
		wasInterrupted, hasState, stored := einotool.GetInterruptState[approvalInterruptState](ctx)
		if !wasInterrupted {
			created, err := r.approvals.Create(ctx, approval.Request{
				WorkspaceID: r.workspaceID, RunID: r.runID, ToolCallID: input.CallID,
				ToolVersionID: binding.VersionID, Arguments: []byte(input.Arguments),
			})
			if err != nil {
				return fmt.Errorf("create approval: %w", err)
			}
			info := approvalInterruptInfo{
				ApprovalID: created.ID, RunID: r.runID, ToolName: input.Name, ToolCallID: input.CallID,
				ToolVersionID: binding.VersionID, Arguments: input.Arguments,
			}
			if r.activeSkill != nil {
				info.ActiveSkillName = r.activeSkill()
			}
			return einotool.StatefulInterrupt(ctx, info, approvalInterruptState{Info: info})
		}
		if !hasState {
			return fmt.Errorf("approval interrupt state is missing")
		}
		if stored.Info.ActiveSkillName != "" && r.restoreSkill != nil {
			if err := r.restoreSkill(stored.Info.ActiveSkillName); err != nil {
				return err
			}
		}
		isTarget, hasData, decision := einotool.GetResumeContext[approvalResumeDecision](ctx)
		if !isTarget {
			return einotool.StatefulInterrupt(ctx, stored.Info, stored)
		}
		if !hasData || !decision.Approved {
			return fmt.Errorf("approval was not granted")
		}
		return nil
	}
}

// persistInterrupts 找到根中断，把“中断 ID + Eino 检查点”持久化到对应审批单。
func (r *ADKRunner) persistInterrupts(ctx context.Context, checkpoint []byte, interrupts []*adk.InterruptCtx) error {
	if len(checkpoint) == 0 {
		return fmt.Errorf("eino did not produce an approval checkpoint")
	}
	for _, interrupt := range interrupts {
		if interrupt == nil || !interrupt.IsRootCause {
			continue
		}
		info, ok := approvalInfo(interrupt.Info)
		if !ok {
			continue
		}
		payload, err := json.Marshal(persistedFrameworkCheckpoint{
			Version: frameworkCheckpointVersion, InterruptID: interrupt.ID,
			ActiveSkillName: info.ActiveSkillName, Data: checkpoint,
		})
		if err != nil {
			return err
		}
		if err := r.approvals.SaveCheckpoint(ctx, r.workspaceID, info.ApprovalID, payload); err != nil {
			return err
		}
		return &AwaitingApprovalError{
			ApprovalID: info.ApprovalID, ToolName: info.ToolName,
			ToolCallID: info.ToolCallID, ToolVersionID: info.ToolVersionID,
		}
	}
	return fmt.Errorf("approval interruption has no root cause")
}

func approvalInfo(value any) (approvalInterruptInfo, bool) {
	switch info := value.(type) {
	case approvalInterruptInfo:
		return info, true
	case *approvalInterruptInfo:
		if info != nil {
			return *info, true
		}
	}
	return approvalInterruptInfo{}, false
}

func consumeADKEvents(iterator *adk.AsyncIterator[*adk.AgentEvent]) (*schema.Message, []*adk.InterruptCtx, error) {
	var answer *schema.Message
	var interrupted []*adk.InterruptCtx
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return nil, nil, event.Err
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			interrupted = append(interrupted, event.Action.Interrupted.InterruptContexts...)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			return nil, nil, err
		}
		if message != nil && message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
			answer = message
		}
	}
	return answer, interrupted, nil
}

// frameworkCheckpointStore 实现 Eino CheckPointStore；每次运行只需保留最新快照。
type frameworkCheckpointStore struct {
	mu      sync.RWMutex
	initial map[string][]byte
	latest  []byte
}

// newFrameworkCheckpointStore 把数据库读出的检查点预装到 Eino 所需的存储接口中。
func newFrameworkCheckpointStore(id string, data []byte) *frameworkCheckpointStore {
	store := &frameworkCheckpointStore{}
	if id != "" && len(data) > 0 {
		store.initial = map[string][]byte{id: append([]byte(nil), data...)}
	}
	return store
}

func (s *frameworkCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.initial[id]
	return append([]byte(nil), data...), ok, nil
}

func (s *frameworkCheckpointStore) Set(_ context.Context, _ string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = append(s.latest[:0], data...)
	return nil
}

func (s *frameworkCheckpointStore) Latest() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.latest...)
}

// toolCallIDKey 是只在本包内使用的 Context key 类型。
// 中间件用它把 Eino 生成的 CallID 传给 bindingTool，避免增加 Eino 工具接口之外的参数。
type toolCallIDKey struct{}

// bindingTool 是本项目工具系统到 Eino InvokableTool 的适配器。
type bindingTool struct {
	binding     ToolBinding
	executor    ToolExecutor
	workspaceID string
}

// Info 返回给 Eino/大模型的工具说明，包括工具名、描述和参数 JSON Schema。
// 模型依据这些信息决定是否调用工具以及应生成哪些参数。
func (t *bindingTool) Info(context.Context) (*schema.ToolInfo, error) {
	if t.binding.Info == nil {
		return nil, fmt.Errorf("tool %s has no schema", t.binding.Name)
	}
	info := *t.binding.Info
	info.Name = t.binding.Name
	return &info, nil
}

// InvokableRun 接收模型生成的 JSON 参数，并转交项目统一的 Executor 执行。
// 对 Eino 而言它只是一个可调用工具；至于底层是 HTTP 还是沙箱，由 Executor 决定。
func (t *bindingTool) InvokableRun(ctx context.Context, arguments string, _ ...einotool.Option) (string, error) {
	callID, _ := ctx.Value(toolCallIDKey{}).(string)
	result, err := t.executor.Execute(ctx, tooling.Call{
		WorkspaceID: t.workspaceID, ToolVersionID: t.binding.VersionID,
		// 同一个 Eino ToolCall 使用稳定的 CallID 构造幂等键，避免重试时重复产生业务副作用。
		Arguments: []byte(arguments), IdempotencyKey: "react:" + callID,
	})
	if err != nil {
		return "", err
	}
	return string(result.Body), nil
}

// toolEventMiddleware 包裹每次工具调用：执行前后向 SSE 链路发送事件，
// 同时把 Eino 的 CallID 放进 Context，供 bindingTool 构造幂等键。
func toolEventMiddleware(
	emit Emitter,
	authorize func(name, arguments string) error,
	interrupt func(context.Context, *compose.ToolInput) error,
) compose.ToolMiddleware {
	return compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if emit != nil {
				if err := emitContext(ctx, emit, Event{Type: "tool_started", Data: map[string]any{"name": input.Name, "call_id": input.CallID}}); err != nil {
					return nil, err
				}
			}
			// 不只依赖“模型能看到哪些工具”：执行前再校验一次，防止越权调用。
			var output *compose.ToolOutput
			var err error
			if authorizeErr := authorizeTool(authorize, input); authorizeErr != nil {
				output = &compose.ToolOutput{Result: fmt.Sprintf(`{"error":%q}`, authorizeErr.Error())}
			} else if interruptErr := interrupt(ctx, input); interruptErr != nil {
				// 中断必须原样返回给 Eino，不能像普通工具错误一样转换为 JSON 结果。
				return nil, interruptErr
			} else {
				// next 才是被包装的真实工具调用。
				output, err = next(context.WithValue(ctx, toolCallIDKey{}, input.CallID), input)
			}
			if err != nil {
				if _, isInterrupt := compose.IsInterruptRerunError(err); isInterrupt {
					return nil, err
				}
				// 把工具错误转换成一段工具结果并清空 error，让 ReAct 循环继续，
				// 这样模型有机会看到错误、调整参数或向用户解释，而不是让整个运行立即中断。
				output, err = &compose.ToolOutput{Result: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
			}
			if emit != nil {
				if emitErr := emitContext(ctx, emit, Event{Type: "tool_finished", Data: map[string]any{"name": input.Name, "call_id": input.CallID}}); emitErr != nil {
					return nil, emitErr
				}
			}
			return output, err
		}
	}}
}

func authorizeTool(authorize func(name, arguments string) error, input *compose.ToolInput) error {
	if authorize == nil {
		return nil
	}
	return authorize(input.Name, input.Arguments)
}
