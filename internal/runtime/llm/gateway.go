// Package llm 把模型 SDK 收敛为 Runtime 使用的稳定接口。
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/config"
)

type Gateway struct {
	model model.BaseChatModel
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

	return &Gateway{model: chatModel}, nil
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
