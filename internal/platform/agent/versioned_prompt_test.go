package agent_test

import (
	"context"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
)

const testModelConfigVersionID = "model-config-v1"

type testModelValidator struct{}

func (testModelValidator) ValidateConfigVersion(context.Context, string, string) error { return nil }

func newVersionedSystemPrompt(t *testing.T, workspaceID string) (*prompt.Service, string) {
	t.Helper()
	service := prompt.NewService(platform.NewMemoryPromptStore(), promptcache.NewCache())
	_, v, err := service.CreatePrompt(context.Background(), prompt.CreatePromptRequest{
		WorkspaceID: workspaceID, Name: "test-system", Category: "test-system",
		Template: "你是测试助手", VariablesSchema: `{}`, CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("create versioned system prompt: %v", err)
	}
	return service, v.ID
}
