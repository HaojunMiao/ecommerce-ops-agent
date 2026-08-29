package ecommerceops_test

import (
	"os"
	"strings"
	"testing"
)

func TestDeploymentWorkflowTargetsStaySeparated(t *testing.T) {
	makefileBytes, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(makefileBytes)
	checks := []string{
		"crossborder-install: ## 向 make up 启动的完整环境安装电商场景",
		"KBOT_URL=$(PLATFORM_URL)",
		"crossborder-install-isolated: ## 向 make crossborder-up 启动的独立环境安装电商场景",
		"KBOT_URL=$(CROSSBORDER_ISOLATED_URL)",
		"bootstrap-model-config: ## 在 make up 的完整环境",
		"$(LANGFUSE_COMPOSE) run --rm --build --entrypoint /ecommerce-ops-bootstrap-model-config",
		"bootstrap-model-config-lite: ## 在 make up-lite 的轻量环境",
		"$(LITE_COMPOSE) run --rm --build --entrypoint /ecommerce-ops-bootstrap-model-config",
		"crossborder-bootstrap-model-config: ## 在 make crossborder-up 的独立环境",
		"$(CROSSBORDER_COMPOSE) run --rm --build --entrypoint /ecommerce-ops-bootstrap-model-config",
	}
	for _, check := range checks {
		if !strings.Contains(makefile, check) {
			t.Errorf("Makefile deployment contract missing %q", check)
		}
	}
}

func TestCrossborderComposeUsesAnExplicitScenarioOverlay(t *testing.T) {
	makefileBytes, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(makefileBytes)
	if !strings.Contains(makefile, "projects/crossborder/platform-compose.yml") {
		t.Fatal("crossborder compose must use the scenario overlay")
	}
	if strings.Contains(makefile, "projects/crossborder/kbot-compose.yml") {
		t.Fatal("legacy course compose filename must not return")
	}
	if _, err := os.Stat("projects/crossborder/platform-compose.yml"); err != nil {
		t.Fatalf("scenario compose overlay missing: %v", err)
	}
}
