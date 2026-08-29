package llm

import (
	"context"
	"strings"
	"testing"
)

func TestPrepareExecutionRejectsMissingModelConfigVersion(t *testing.T) {
	_, err := NewGateway().PrepareExecution(context.Background())
	if err == nil || !strings.Contains(err.Error(), "model_config_version_id is required") {
		t.Fatalf("missing model config version error = %v", err)
	}
}

func TestPrepareExecutionRejectsMissingResolver(t *testing.T) {
	ctx := WithInvocationConfig(context.Background(), InvocationConfig{ModelConfigVersionID: "config-v1"})
	_, err := NewGateway().PrepareExecution(ctx)
	if err == nil || !strings.Contains(err.Error(), "resolver is not configured") {
		t.Fatalf("missing resolver error = %v", err)
	}
}

type staticConfigResolver struct {
	config *ResolvedModelConfig
}

func (r staticConfigResolver) ResolveConfig(context.Context, string) (*ResolvedModelConfig, error) {
	return r.config, nil
}

func TestPrepareExecutionUsesConfigRetryPolicy(t *testing.T) {
	g := &Gateway{sink: NopSink{}}
	g.WithConfigResolver(staticConfigResolver{config: &ResolvedModelConfig{
		ID: "config-v1", BaseURL: "https://primary.example/v1", APIKey: "test", Model: "model-a", MaxRetries: 1,
	}})
	ctx := WithInvocationConfig(context.Background(), InvocationConfig{ModelConfigVersionID: "config-v1"})

	plan, err := g.PrepareExecution(ctx)
	if err != nil {
		t.Fatalf("prepare execution: %v", err)
	}
	if plan.Model == nil {
		t.Fatal("model is nil")
	}
	if plan.Retry == nil || plan.Retry.MaxRetries != 1 {
		t.Fatalf("retry config = %+v", plan.Retry)
	}
}
