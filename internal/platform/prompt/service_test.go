package prompt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
)

type failingVersionStore struct {
	prompt.Store
}

func (failingVersionStore) CreatePromptVersion(context.Context, *domain.PromptVersion) error {
	return errors.New("create version failed")
}

func newService() *prompt.Service {
	return prompt.NewService(platform.NewMemoryPromptStore(), promptcache.NewCache())
}

func TestCreatePromptCreatesImmutableV1(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	_, v, err := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "w1", Name: "greeting",
		Template: "你好 {{.user_name}}", VariablesSchema: `{"required":["user_name"]}`,
		CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v.Version != 1 || v.TokenEstimate <= 0 {
		t.Fatalf("unexpected v1: %+v", v)
	}

	got, err := svc.RenderByVersion(ctx, v.ID, map[string]any{"user_name": "小明"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "你好 小明" {
		t.Fatalf("unexpected render: %q", got)
	}
}

func TestCreatePromptPrevalidatesBeforeWrite(t *testing.T) {
	store := platform.NewMemoryPromptStore()
	svc := prompt.NewService(store, promptcache.NewCache())

	if _, _, err := svc.CreatePrompt(context.Background(), prompt.CreatePromptRequest{
		WorkspaceID: "w1", Name: "invalid", Template: "你好 {{name}}", CreatedBy: "u1",
	}); err == nil {
		t.Fatal("expected invalid Go template variable to fail")
	}
	prompts, err := store.ListPrompts(context.Background(), "w1")
	if err != nil || len(prompts) != 0 {
		t.Fatalf("invalid create left orphan prompt: %+v err=%v", prompts, err)
	}
}

func TestCreatePromptRollsBackWhenVersionCreateFails(t *testing.T) {
	baseStore := platform.NewMemoryPromptStore()
	store := failingVersionStore{Store: baseStore}
	svc := prompt.NewService(store, promptcache.NewCache())

	if _, _, err := svc.CreatePrompt(context.Background(), prompt.CreatePromptRequest{
		WorkspaceID: "w1", Name: "rollback", Template: "你好 {{.name}}", CreatedBy: "u1",
	}); err == nil {
		t.Fatal("expected publish failure")
	}
	prompts, err := baseStore.ListPrompts(context.Background(), "w1")
	if err != nil || len(prompts) != 0 {
		t.Fatalf("failed create left orphan prompt: %+v err=%v", prompts, err)
	}
}

func TestMissingVariableErrors(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	_, v, _ := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "w1", Name: "g", Template: "你好 {{.user_name}}",
		VariablesSchema: `{"required":["user_name"]}`, CreatedBy: "u1",
	})
	_, err := svc.RenderByVersion(ctx, v.ID, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
}

func TestVersionsRenderIndependently(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	p, v1, _ := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "w1", Name: "g", Template: "v1 内容", CreatedBy: "u1",
	})

	// 新增 v2 不改变 v1。
	v2, err := svc.CreateVersion(ctx, p.ID, "v2 内容", "", "u1")
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	got, _ := svc.RenderByVersion(ctx, v1.ID, nil)
	if got != "v1 内容" {
		t.Fatalf("expected v1, got %q", got)
	}
	got, _ = svc.RenderByVersion(ctx, v2.ID, nil)
	if got != "v2 内容" {
		t.Fatalf("expected v2, got %q", got)
	}
}

func TestDiff(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	p, _, _ := svc.CreatePrompt(ctx, prompt.CreatePromptRequest{
		WorkspaceID: "w1", Name: "g", Template: "line1\nline2\nline3", CreatedBy: "u1",
	})
	_, _ = svc.CreateVersion(ctx, p.ID, "line1\nline2-changed\nline3", "", "u1")

	diff, err := svc.Diff(ctx, p.ID, 1, 2)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "- line2") || !strings.Contains(diff, "+ line2-changed") {
		t.Fatalf("unexpected diff:\n%s", diff)
	}
	if !strings.Contains(diff, "  line1") {
		t.Fatalf("expected common line1 unchanged:\n%s", diff)
	}
}
