//go:build integration

package modelconfig_test

import (
	"context"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
)

func TestPostgresModelConfigRoundTrip(t *testing.T) {
	db := testpg.Start(t)
	ctx := context.Background()
	_, _ = db.Exec(ctx, `TRUNCATE model_config_versions CASCADE`)
	svc := modelconfig.NewService(modelconfig.NewPostgresStore(db))
	svc.SetCredential(modelconfig.DefaultCredentialRef, "sk-integration")
	v, err := svc.EnsureConfigVersion(ctx, modelconfig.EnsureConfigRequest{
		WorkspaceID: "w-model", Name: "main", BaseURL: "https://example.com/v1", ModelName: "test-model",
		InputPricePerMillion: 1, CachedInputPricePerMillion: .2, OutputPricePerMillion: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := svc.ResolveConfig(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != "sk-integration" || resolved.InputPricePerMillion != 1 || resolved.OutputPricePerMillion != 3 {
		t.Fatalf("unexpected resolved config: %+v", resolved)
	}
}
