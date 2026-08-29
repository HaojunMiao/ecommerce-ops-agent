// Package llm 提供LLM网关实现
package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// provider 是一路可调用的模型配置。
type provider struct {
	model                      model.ToolCallingChatModel
	system                     string // gen_ai.system:openai-compatible
	modelID                    string // gen_ai.request.model
	inputPricePerMillion       float64
	outputPricePerMillion      float64
	cachedInputPricePerMillion float64
}

// ResolvedModelConfig is one immutable and directly callable model configuration.
type ResolvedModelConfig struct {
	ID                         string
	ProviderKind               string
	BaseURL                    string
	APIKey                     string
	Model                      string
	TimeoutMS                  int
	MaxRetries                 int
	InputPricePerMillion       float64
	OutputPricePerMillion      float64
	CachedInputPricePerMillion float64
}

type ConfigResolver interface {
	ResolveConfig(ctx context.Context, versionID string) (*ResolvedModelConfig, error)
}

// Gateway LLM网关
//

// 把 Eino 的 ToolCallingChatModel 收敛在一处，上层只依赖稳定方法。
type Gateway struct {
	sink      ModelCallSink // 调用计量落库(model_call_logs);默认 NopSink
	configs   ConfigResolver
	models    sync.Map // model config version/generation config -> provider
	endpoints interface {
		ValidateURL(context.Context, string) error
		HTTPClient(time.Duration) *http.Client
	}
}

// ExecutionPlan 把一次 Agent 运行所需的模型与重试策略交给 Eino ADK。
type ExecutionPlan struct {
	Model model.BaseChatModel
	Retry *adk.ModelRetryConfig
}

// NewGateway 创建只按不可变模型配置版本路由的网关。
// 部署环境只提供 CredentialRef 对应的密钥，不再构成第二套模型配置来源。
func NewGateway() *Gateway { return &Gateway{sink: NopSink{}} }

// WithCallSink 注入调用计量落库器(db != nil 时为 PgModelCallSink)。返回自身便于链式。
func (g *Gateway) WithCallSink(sink ModelCallSink) *Gateway {
	if sink != nil {
		g.sink = sink
	}
	return g
}

func (g *Gateway) WithConfigResolver(resolver ConfigResolver) *Gateway {
	g.configs = resolver
	return g
}

func (g *Gateway) WithEndpointPolicy(policy interface {
	ValidateURL(context.Context, string) error
	HTTPClient(time.Duration) *http.Client
}) *Gateway {
	g.endpoints = policy
	return g
}

// PrepareExecution resolves one immutable model configuration and builds Eino's execution options.
func (g *Gateway) PrepareExecution(ctx context.Context) (*ExecutionPlan, error) {
	classification := classificationFromContext(ctx)
	inv := invocationFromContext(ctx)
	candidate, err := g.providerFor(ctx, inv)
	if err != nil {
		return nil, err
	}
	plan := &ExecutionPlan{Model: &managedModel{
		gateway: g, provider: candidate.provider, invocation: inv, classification: classification,
	}}
	if candidate.retries > 0 {
		plan.Retry = &adk.ModelRetryConfig{MaxRetries: candidate.retries}
	}
	return plan, nil
}

type providerCandidate struct {
	provider provider
	retries  int
}

func (g *Gateway) providerFor(ctx context.Context, inv InvocationConfig) (providerCandidate, error) {
	if inv.ModelConfigVersionID == "" {
		return providerCandidate{}, fmt.Errorf("model_config_version_id is required")
	}
	if g.configs == nil {
		return providerCandidate{}, fmt.Errorf("model config resolver is not configured")
	}
	resolved, err := g.configs.ResolveConfig(ctx, inv.ModelConfigVersionID)
	if err != nil {
		return providerCandidate{}, fmt.Errorf("resolve model config: %w", err)
	}
	p, err := g.dynamicProvider(ctx, resolved, inv.GenerationConfig)
	if err != nil {
		return providerCandidate{}, err
	}
	return providerCandidate{provider: p, retries: resolved.MaxRetries}, nil
}

func (g *Gateway) dynamicProvider(ctx context.Context, d *ResolvedModelConfig, cfg domain.GenerationConfig) (provider, error) {
	if g.endpoints != nil {
		if err := g.endpoints.ValidateURL(ctx, d.BaseURL); err != nil {
			return provider{}, fmt.Errorf("validate model config %s endpoint: %w", d.ID, err)
		}
	}
	cacheInput, _ := json.Marshal(struct {
		Config     ResolvedModelConfig
		Generation domain.GenerationConfig
	}{Config: *d, Generation: cfg})
	cacheHash := sha256.Sum256(cacheInput)
	key := fmt.Sprintf("%s:%x", d.ID, cacheHash)
	if cached, ok := g.models.Load(key); ok {
		return cached.(provider), nil
	}
	timeout := time.Duration(d.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	modelConfig := &openai.ChatModelConfig{
		APIKey: d.APIKey, BaseURL: d.BaseURL, Model: d.Model, Timeout: timeout,
		MaxCompletionTokens: cfg.MaxOutputTokens, Temperature: cfg.Temperature,
		TopP: cfg.TopP, Stop: cfg.Stop, Seed: cfg.Seed,
	}
	if g.endpoints != nil {
		modelConfig.HTTPClient = g.endpoints.HTTPClient(timeout)
	}
	m, err := openai.NewChatModel(ctx, modelConfig)
	if err != nil {
		return provider{}, fmt.Errorf("create model config %s: %w", d.ID, err)
	}
	p := provider{
		model: m, system: d.ProviderKind, modelID: d.Model,
		inputPricePerMillion: d.InputPricePerMillion, outputPricePerMillion: d.OutputPricePerMillion,
		cachedInputPricePerMillion: d.CachedInputPricePerMillion,
	}
	g.models.Store(key, p)
	return p, nil
}

// managedModel 包装模型调用，补充追踪、成本与审计。
type managedModel struct {
	gateway        *Gateway
	provider       provider
	invocation     InvocationConfig
	classification string
}

func (m *managedModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var response *schema.Message
	startedAt := time.Now()
	result, callErr := withSpan(ctx, m.provider.system, m.provider.modelID, func(ctx context.Context) (callResult, error) {
		generated, err := m.provider.model.Generate(ctx, messages, opts...)
		if err != nil {
			return callResult{input: marshalJSON(messages)}, err
		}
		response = generated
		return modelCallResult(messages, generated), nil
	})
	m.finish(ctx, result, startedAt, callErr)
	if callErr != nil {
		return nil, callErr
	}
	return response, nil
}

func (m *managedModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	inputStream, err := m.provider.model.Stream(ctx, messages, opts...)
	if err != nil {
		m.finish(ctx, callResult{input: marshalJSON(messages)}, time.Now(), err)
		return nil, err
	}
	output, writer := schema.Pipe[*schema.Message](1)
	startedAt := time.Now()
	go func() {
		defer writer.Close()
		defer inputStream.Close()
		chunks := make([]*schema.Message, 0, 8)
		result, callErr := withSpan(ctx, m.provider.system, m.provider.modelID, func(context.Context) (callResult, error) {
			for {
				chunk, recvErr := inputStream.Recv()
				if errors.Is(recvErr, io.EOF) {
					break
				}
				if recvErr != nil {
					return callResult{input: marshalJSON(messages)}, recvErr
				}
				chunks = append(chunks, chunk)
				if writer.Send(chunk, nil) {
					return callResult{input: marshalJSON(messages)}, context.Canceled
				}
			}
			merged, concatErr := schema.ConcatMessages(chunks)
			if concatErr != nil {
				return callResult{input: marshalJSON(messages)}, concatErr
			}
			return modelCallResult(messages, merged), nil
		})
		m.finish(context.WithoutCancel(ctx), result, startedAt, callErr)
		if callErr != nil {
			writer.Send(nil, callErr)
		}
	}()
	return output, nil
}

func (m *managedModel) finish(ctx context.Context, result callResult, startedAt time.Time, callErr error) {
	actualCost := tokenCost(m.provider, result.inputTokens, result.outputTokens, result.cachedTokens)
	status := "ok"
	if callErr != nil {
		status = "error"
	}
	m.gateway.sink.Record(ctx, CallUsage{
		Provider: m.provider.system,
		Model:    m.provider.modelID, InputTokens: result.inputTokens, OutputTokens: result.outputTokens,
		CachedTokens: result.cachedTokens, LatencyMs: int(time.Since(startedAt).Milliseconds()),
		Status: status, Classification: m.classification, WorkspaceID: m.invocation.WorkspaceID,
		AgentID: m.invocation.AgentID, UserID: m.invocation.UserID, PromptVersionID: m.invocation.PromptVersionID,
		ModelConfigVersionID: m.invocation.ModelConfigVersionID, Cost: actualCost,
	})
}

func modelCallResult(messages []*schema.Message, response *schema.Message) callResult {
	result := callResult{input: marshalJSON(messages), output: marshalJSON(response)}
	if response == nil || response.ResponseMeta == nil {
		return result
	}
	result.finishReason = response.ResponseMeta.FinishReason
	if response.ResponseMeta.Usage != nil {
		result.inputTokens = response.ResponseMeta.Usage.PromptTokens
		result.outputTokens = response.ResponseMeta.Usage.CompletionTokens
		result.cachedTokens = response.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
	}
	return result
}

func marshalJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func tokenCost(p provider, inputTokens, outputTokens, cachedTokens int) float64 {
	uncached := inputTokens - cachedTokens
	if uncached < 0 {
		uncached = 0
	}
	return (float64(uncached)*p.inputPricePerMillion +
		float64(cachedTokens)*p.cachedInputPricePerMillion +
		float64(outputTokens)*p.outputPricePerMillion) / 1_000_000
}
