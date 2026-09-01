package llm

import (
	"context"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
)

// 会话数据分级仅作为模型调用的审计元数据，不参与模型选择。
type classificationKey struct{}
type invocationKey struct{}

// WithClassification 把会话数据分级写入 context，供调用日志记录。
func WithClassification(ctx context.Context, classification string) context.Context {
	if classification == "" {
		return ctx
	}
	return context.WithValue(ctx, classificationKey{}, classification)
}

func classificationFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(classificationKey{}).(string); ok {
		return v
	}
	return ""
}

// InvocationConfig 是从会话固定的 AgentVersion 解析出的一次模型调用配置。
type InvocationConfig struct {
	WorkspaceID          string
	Environment          string
	AgentID              string
	UserID               string
	PromptVersionID      string
	ModelConfigVersionID string
	GenerationConfig     domain.GenerationConfig
}

func WithInvocationConfig(ctx context.Context, cfg InvocationConfig) context.Context {
	return context.WithValue(ctx, invocationKey{}, cfg)
}

func invocationFromContext(ctx context.Context) InvocationConfig {
	v, _ := ctx.Value(invocationKey{}).(InvocationConfig)
	return v
}
