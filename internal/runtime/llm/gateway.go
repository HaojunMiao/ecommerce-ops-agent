// Package llm 把模型 SDK 收敛为 Runtime 使用的稳定接口，并构造 Eino ADK 模型执行策略。
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
)

// ExecutionPlan 是已解析的本次运行计划：主模型负责首次调用，Retry 和 Failover
// 直接交给 Eino ADK 在 ReAct 迭代中执行。
type ExecutionPlan struct {
	Model    model.BaseChatModel
	Retry    *adk.ModelRetryConfig
	Failover *adk.ModelFailoverConfig[*schema.Message]
}

type Gateway struct {
	model   model.BaseChatModel
	timeout time.Duration
}

func NewGateway(cfg config.Config) (*Gateway, error) {
	// 初始化大模型连接
	// 拿到一个chatmodel，给到Gateway的model字段
	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		APIKey:  cfg.LLMAPIKey,
		BaseURL: cfg.LLMBaseURL,
		Model:   cfg.LLMModel,
		Timeout: cfg.LLMTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model failed, err:%w", err)
	}

	return &Gateway{model: chatModel, timeout: cfg.LLMTimeout}, nil
}

// PrepareExecution 把模型配置版本中的有序部署转换为 Eino ADK 的重试和故障切换策略。
// 第一个部署是主模型，后续部署按顺序作为备用模型。
func (g *Gateway) PrepareExecution(ctx context.Context, profile modelconfig.ProfileVersion) (*ExecutionPlan, error) {
	if g == nil || len(profile.Deployments) == 0 {
		return nil, fmt.Errorf("model profile has no deployments")
	}
	models := make([]model.BaseChatModel, 0, len(profile.Deployments))
	maxRetries := 0
	for index, deployment := range profile.Deployments {
		if !supportedProvider(deployment.Provider) {
			return nil, fmt.Errorf("deployment %d: unsupported provider %q", index, deployment.Provider)
		}
		selected, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
			APIKey: deployment.APIKey, BaseURL: deployment.BaseURL, Model: deployment.Model, Timeout: g.timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("deployment %d: %w", index, err)
		}
		models = append(models, selected)
		if deployment.MaxRetries > maxRetries {
			maxRetries = deployment.MaxRetries
		}
	}
	plan := &ExecutionPlan{Model: models[0]}
	if maxRetries > 0 {
		plan.Retry = &adk.ModelRetryConfig{MaxRetries: maxRetries}
	}
	if len(models) > 1 {
		plan.Failover = &adk.ModelFailoverConfig[*schema.Message]{
			MaxRetries: uint(len(models) - 1),
			ShouldFailover: func(_ context.Context, _ *schema.Message, callErr error) bool {
				return callErr != nil
			},
			GetFailoverModel: func(_ context.Context, failover *adk.FailoverContext[*schema.Message]) (model.BaseChatModel, []*schema.Message, error) {
				index := int(failover.FailoverAttempt)
				if index <= 0 || index >= len(models) {
					return nil, nil, fmt.Errorf("model failover attempt %d is out of range", index)
				}
				return models[index], nil, nil
			},
		}
	}
	return plan, nil
}

func supportedProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai-compatible", "openai", "deepseek", "doubao":
		return true
	default:
		return false
	}
}

func (g *Gateway) Generate(
	ctx context.Context,
	messages []*schema.Message,
	opts ...model.Option,
) (*schema.Message, error) {
	if g == nil || g.model == nil {
		return nil, fmt.Errorf("chat model is required")
	}
	// 调用eino的Generate
	resp, err := g.model.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, fmt.Errorf("generate resp failed, err:%w", err)
	}
	return resp, nil
}

// 类似Generate，相当于包了一层Gateway而已
// 和eino chatmodel提供的Stream()的参数都一样，就是调用eino的Stream()
func (g *Gateway) Stream(
	ctx context.Context,
	messages []*schema.Message,
	opts ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	if g == nil || g.model == nil {
		return nil, fmt.Errorf("chat model is required")
	}
	stream, err := g.model.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, fmt.Errorf("stream resp failed, err:%w", err)
	}
	return stream, nil
}
