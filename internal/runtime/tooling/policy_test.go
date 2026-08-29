package tooling

import (
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
)

func TestToolPolicyMetadata(t *testing.T) {
	if !sourceRequiresNetwork("rest_api") {
		t.Fatal("REST tools require network authorization")
	}
	if sourceRequiresNetwork("internal_sdk") {
		t.Fatal("local tool types should not require network")
	}
	if !isKBScopedTool(&tool.ToolConfig{
		SourceType: "internal_sdk", EndpointConfig: map[string]interface{}{"sdk_name": "search_knowledge_base"},
	}) {
		t.Fatal("KB search tool must be scoped")
	}
}
