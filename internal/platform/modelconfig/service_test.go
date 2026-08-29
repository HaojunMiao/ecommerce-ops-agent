package modelconfig_test

import (
	"context"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
)

func TestConfigRejectsPrivateEndpoint(t *testing.T) {
	svc := modelconfig.NewService(modelconfig.NewMemoryStore())
	svc.ConfigureEndpointPolicy(tool.NewEndpointPolicy(nil, false))
	_, err := svc.EnsureConfigVersion(context.Background(), modelconfig.EnsureConfigRequest{
		WorkspaceID: "w1", Name: "main", BaseURL: "http://127.0.0.1:8080/v1", ModelName: "model-a",
	})
	if err == nil {
		t.Fatal("private model endpoint should be rejected")
	}
}

func TestImmutableConfigVersionAndEnvironmentCredential(t *testing.T) {
	ctx := context.Background()
	svc := modelconfig.NewService(modelconfig.NewMemoryStore())
	svc.SetCredential(modelconfig.DefaultCredentialRef, "sk-first")
	req := modelconfig.EnsureConfigRequest{
		WorkspaceID: "w1", Name: "main", BaseURL: "https://example.com/v1", ModelName: "model-a",
		InputPricePerMillion: 1.25, OutputPricePerMillion: 2.5, CachedInputPricePerMillion: .25,
	}
	v1, err := svc.EnsureConfigVersion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	same, err := svc.EnsureConfigVersion(ctx, req)
	if err != nil || same.ID != v1.ID {
		t.Fatalf("matching config should be idempotent: got=%+v err=%v", same, err)
	}
	req.ModelName = "model-b"
	v2, err := svc.EnsureConfigVersion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if v2.ID == v1.ID || v2.Version != 2 {
		t.Fatalf("changed settings should append v2: v1=%+v v2=%+v", v1, v2)
	}
	resolved, err := svc.ResolveConfig(ctx, v1.ID)
	if err != nil || resolved.APIKey != "sk-first" || resolved.Model != "model-a" {
		t.Fatalf("unexpected resolved config: %+v err=%v", resolved, err)
	}
	svc.SetCredential(modelconfig.DefaultCredentialRef, "sk-rotated")
	resolved, err = svc.ResolveConfig(ctx, v1.ID)
	if err != nil || resolved.APIKey != "sk-rotated" {
		t.Fatalf("environment credential rotation did not take effect: %+v err=%v", resolved, err)
	}
}

func TestConfigVersionWorkspaceValidation(t *testing.T) {
	ctx := context.Background()
	svc := modelconfig.NewService(modelconfig.NewMemoryStore())
	v, err := svc.EnsureConfigVersion(ctx, modelconfig.EnsureConfigRequest{
		WorkspaceID: "w1", Name: "main", BaseURL: "https://example.com/v1", ModelName: "model-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ValidateConfigVersion(ctx, "w2", v.ID); err == nil {
		t.Fatal("cross-workspace model config should be rejected")
	}
	if err := svc.ValidateConfigVersion(ctx, "w1", ""); err == nil {
		t.Fatal("empty model config version should be rejected")
	}
}
