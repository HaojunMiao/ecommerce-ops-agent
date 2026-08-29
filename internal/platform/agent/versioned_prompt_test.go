package agent_test

import (
	"context"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
)

func newVersionedSystemPrompt(t *testing.T, workspaceID string) (*prompt.Service, string) {
	t.Helper()
	service := prompt.NewService(platform.NewMemoryPromptStore(), promptcache.NewCache(), prompt.NoopPublisher{})
	p, _, err := service.CreatePrompt(context.Background(), prompt.CreatePromptRequest{
		WorkspaceID: workspaceID, Name: "test-system", Category: "test-system",
		Template: "你是测试助手", VariablesSchema: `{}`, ModelConfigVersionID: "model-config-v1", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("create versioned system prompt: %v", err)
	}
	return service, p.ID
}
