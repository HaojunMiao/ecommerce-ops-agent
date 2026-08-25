package engine

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/tooling"
)

// ToolBinding 直接复用 tooling.Binding。
// 它描述的是“本次 Agent 运行允许使用的某个固定版本工具”，其中 Info 会提供给大模型，
// VersionID 则在真正执行时用于从平台注册表中找到那个不可变版本。
type ToolBinding = tooling.Binding

// ToolExecutor 是 ReAct 层对工具执行器的最小依赖。
// react.go 只要求它能够执行一次工具调用，不关心底层调用的是 REST 服务还是沙箱。
type ToolExecutor interface {
	Execute(ctx context.Context, call tooling.Call) (tooling.Result, error)
}

// ADKRunner 负责把本项目的平台对象装配成 Eino ADK 能运行的 Agent。
// model 负责推理和决定是否调用工具；executor 负责真正执行工具；
// workspaceID 用于保证工具只能在当前 Workspace 范围内解析和调用。
type ADKRunner struct {
	model       model.BaseChatModel
	executor    ToolExecutor
	workspaceID string
}

func NewADKRunner(chatModel model.BaseChatModel, executor ToolExecutor, workspaceID string) *ADKRunner {
	return &ADKRunner{model: chatModel, executor: executor, workspaceID: workspaceID}
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
	// ReAct 同时需要“做决定的模型”和“执行动作的工具执行器”。
	if r.model == nil || r.executor == nil {
		return nil, fmt.Errorf("chat model and tool executor are required")
	}
	if maxSteps <= 0 {
		return nil, fmt.Errorf("max steps must be positive")
	}

	// 把工具添加到chatModelAgent
	// binding 是项目内部的工具描述，不能直接交给 Eino。
	// bindingTool 是适配器：实现 Eino BaseTool 接口，同时把调用转回本项目的 Executor。
	tools := make([]einotool.BaseTool, 0, len(bindings))
	for _, binding := range bindings {
		tools = append(tools, &bindingTool{
			binding:     binding,
			executor:    r.executor,
			workspaceID: r.workspaceID,
		})
	}

	// 创建ChatModelAgent
	// ChatModelAgent 封装了 ReAct 循环：
	// 1. 把 messages 和工具定义交给模型；
	// 2. 如果模型返回 tool call，则调用对应工具并把结果追加回消息上下文；
	// 3. 再次调用模型，直到得到最终回答或达到 MaxIterations。
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "course_agent",
		Description: "kbot course agent",
		Model:       r.model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				// 顺序执行工具，避免并行工具调用产生难以控制的副作用和顺序问题。
				ExecuteSequentially: true,
				// 中间件在每次工具执行前后发送 tool_started/tool_finished 事件。
				ToolCallMiddlewares: []compose.ToolMiddleware{toolEventMiddleware(emit)},
			}},
		MaxIterations: maxSteps,
	})
	if err != nil {
		return nil, fmt.Errorf("create Eino ChatModelAgent: %w", err)
	}
	// Runner 真正启动 Agent。它返回迭代器，因为一次 ReAct 运行会产生多种中间事件
	// 例如模型请求调用工具、工具返回结果、模型生成最终答案...
	// 在for循环中不断读取Agent运行期间产生的事件(Eino ADK的adk.AgentEvent)
	iterator := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent}).Run(ctx, messages)
	var answer *schema.Message
	for {
		event, ok := iterator.Next() // 取得下一个Agent事件
		if !ok {
			// 结束了（没有更多事件）
			break
		}
		if event.Err != nil {
			return nil, event.Err
		}
		// 并非所有 ADK 事件都有消息输出；这里只关心能够还原为 Message 的事件。
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			return nil, err
		}
		// Assistant 消息中仍带 ToolCalls，表示模型还在请求执行工具，并不是最终回答。
		// 最终回答必须来自 Assistant，并且不再包含任何 ToolCall。
		if message != nil && message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
			answer = message  // 最终回答赋值给answer
		}
	}
	if answer == nil {
		return nil, fmt.Errorf("Eino ADK run finished without a final answer")
	}
	return answer, nil
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
func toolEventMiddleware(emit Emitter) compose.ToolMiddleware {
	return compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if emit != nil {
				if err := emitContext(ctx, emit, Event{Type: "tool_started", Data: map[string]any{"name": input.Name, "call_id": input.CallID}}); err != nil {
					return nil, err
				}
			}
			// next 才是被包装的真实工具调用。
			output, err := next(context.WithValue(ctx, toolCallIDKey{}, input.CallID), input)
			if err != nil {
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
