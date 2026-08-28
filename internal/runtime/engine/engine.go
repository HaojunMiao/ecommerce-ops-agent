package engine

import (
	"context"
	"fmt"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/audit"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/skill"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/guard"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/tooling"
	"github.com/cloudwego/eino/components/model"
)

// Agent 运行相关 （真正跟大模型打交道的地方）
// 把实际的大模型与 Agent 隔离开，避免将Agent与某个具体的大模型（如Deepseek）绑定
// Runtime 负责“按照已经发布的配置真正运行”。

// type Generator interface {
// 	// 根据 消息 和 Tool定义 生成模型回复
// 	Generate(ctx context.Context, messages []*schema.Message, tools []*schema.ToolInfo) (*schema.Message, error)
// }

// Platform 读取控制面数据的接口
// 可以根据conversationID找到conversation对象，进而通过agentVersionID找到AgentSnapshot
// 注意区分platform.Platform结构体和这个engine.Platform接口
type Platform interface {
	LoadConversation(ctx context.Context, conversationID string) (*domain.Conversation, error)
	GetAgentSnapshotByVersion(ctx context.Context, agentVersionID string) (*AgentSnapshot, error)
}

// AgentSnapshot Runtime使用的解析好的固定Agent配置（此时在运行的版本）
type AgentSnapshot struct {
	ID                    string
	AgentID               string
	WorkspaceID           string
	SystemPrompt          string
	MaxSteps              int
	PromptVersionID       string
	ModelProfileVersionID string
	ToolVersionIDs        []string
	SkillVersionIDs       []string
	KnowledgeVersionIDs   []string
}

// executionPlanner 将已固定的模型配置版本转换为本次运行的主模型、重试和故障切换策略。
type executionPlanner interface {
	PrepareExecution(context.Context, modelconfig.ProfileVersion) (*llm.ExecutionPlan, error)
}

// Engine 依赖接口，不与具体的实现绑定
type Engine struct {
	platform  Platform
	model     model.BaseChatModel
	planner   executionPlanner
	tools     ToolRuntime
	prompts   PromptRenderer
	profiles  ModelProfileResolver
	skills    SkillResolver
	approvals ApprovalGate
	guard     RuntimeGuard
	audit     AuditSink
}

type ToolRuntime interface {
	Bind(ctx context.Context, workspaceID string, versionIDs []string) ([]tooling.Binding, error)
	Execute(ctx context.Context, call tooling.Call) (tooling.Result, error)
}

// PromptRenderer 是运行时解析固定提示词版本所需的最小接口。
type PromptRenderer interface {
	Render(ctx context.Context, workspaceID, versionID string, variables map[string]string) (string, error)
}

// ModelProfileResolver 是运行时读取固定模型配置版本所需的最小接口。
type ModelProfileResolver interface {
	Resolve(ctx context.Context, workspaceID, versionID string) (modelconfig.ProfileVersion, error)
}

// SkillResolver 仅允许运行时按工作空间和固定版本 ID 解析已发布 Skill。
type SkillResolver interface {
	Resolve(ctx context.Context, workspaceID, versionID string) (skill.Version, error)
}

// ConversationMessageStore 是运行时读取和追加多轮消息所需的最小接口。
// Engine 通过接口探测保持兼容：旧 Platform 未实现它时仍可执行单轮对话。
type ConversationMessageStore interface {
	ListMessages(ctx context.Context, workspaceID, conversationID string) ([]domain.Message, error)
	AppendMessage(ctx context.Context, workspaceID, conversationID, role, content string) error
}

// RuntimeGuard 是 Engine 对安全管线的最小依赖；具体规则及存储方式由 guard 包负责。
type RuntimeGuard interface {
	Evaluate(ctx context.Context, workspaceID, hook, text string) (guard.Decision, error)
}

// AuditSink 让运行时依赖最小写入接口，不耦合审计账本的存储实现。
type AuditSink interface {
	Append(ctx context.Context, event audit.Event) (audit.Event, error)
}

// New 创建Agent Runtime
func New(platform Platform, model model.BaseChatModel) *Engine {
	engine := &Engine{
		platform: platform,
		model:    model,
	}
	// Gateway 同时实现 executionPlanner；旧的纯 ChatModel 仍可以继续使用。
	engine.planner, _ = model.(executionPlanner)
	return engine
}

func (e *Engine) WithTools(tools ToolRuntime) *Engine {
	e.tools = tools
	return e
}

// WithRuntimeConfig 注入提示词和模型配置的控制面解析能力。
func (e *Engine) WithRuntimeConfig(prompts PromptRenderer, profiles ModelProfileResolver) *Engine {
	e.prompts, e.profiles = prompts, profiles
	return e
}

func (e *Engine) WithSkills(skills SkillResolver) *Engine {
	e.skills = skills
	return e
}

func (e *Engine) WithApprovals(approvals ApprovalGate) *Engine {
	e.approvals = approvals
	return e
}

func (e *Engine) WithGuard(runtimeGuard RuntimeGuard) *Engine {
	e.guard = runtimeGuard
	return e
}

func (e *Engine) WithAudit(sink AuditSink) *Engine {
	e.audit = sink
	return e
}

// ResolveSnapshot 解析 agent 运行的配置
// 根据会话id找到当时运行的AgentSnapshot（配置）
func (e *Engine) ResolveSnapshot(ctx context.Context, conversationID string) (*AgentSnapshot, error) {
	if e.platform == nil {
		return nil, fmt.Errorf("platform is nil")
	}
	conversation, err := e.platform.LoadConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to load conversation: %w", err)
	}

	if conversation.AgentVersionID == "" {
		return nil, fmt.Errorf("conversation has no agentVersionID")
	}
	snapshot, err := e.platform.GetAgentSnapshotByVersion(ctx, conversation.AgentVersionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent snapshot: %w", err)
	}

	if snapshot.ID != conversation.AgentVersionID {
		return nil, fmt.Errorf(
			"snapshot version mismatch: want %s, got %s",
			conversation.AgentVersionID,
			snapshot.ID,
		)
	}
	if conversation.WorkspaceID != "" && snapshot.WorkspaceID != "" && conversation.WorkspaceID != snapshot.WorkspaceID {
		return nil, fmt.Errorf("conversation and agent snapshot belong to different workspaces")
	}
	return snapshot, nil
}
