package runtime

import (
	"context"
	"fmt"

	"github.com/HaojunMiao/go-agent-platform/internal/domain"
	"github.com/cloudwego/eino/schema"
)

// Agent 运行相关 （真正跟大模型打交道的地方）
// 把实际的大模型与 Agent 隔离开，避免将Agent与某个具体的大模型（如Deepseek）绑定
// Runtime 负责“按照已经发布的配置真正运行”。

type Generator interface {
	// 根据 消息 和 Tool定义 生成模型回复
	Generate(ctx context.Context, messages []*schema.Message, tools []*schema.ToolInfo) (*schema.Message, error)
}

// Platform 读取控制面数据的接口
// 可以根据conversationID找到conversation对象，进而通过agentVersionID找到AgentSnapshot
// 注意区分platform.Platform结构体和这个engine.Platform接口
type Platform interface {
	LoadConversation(ctx context.Context, conversationID string) (*domain.Conversation, error)
	GetAgentSnapshotByVersion(ctx context.Context, agentVersionID string) (*AgentSnapshot, error)
}

// AgentSnapshot Runtime使用的解析好的固定Agent配置（此时在运行的版本）
type AgentSnapshot struct {
	ID           string
	AgentID      string
	WorkspaceID  string
	SystemPrompt string
	MaxSteps     int
}

// Engine 依赖接口，不与具体的实现绑定
type Engine struct {
	platform Platform
	gen      Generator
}

// New 创建Agent Runtime
func New(Platform Platform, gen Generator) *Engine {
	return &Engine{
		platform: Platform,
		gen:      gen,
	}
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
	return snapshot, nil
}
