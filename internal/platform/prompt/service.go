// Package prompt 管理不可变提示词版本。Prompt 是版本化组件，Agent 才是发布单元。
package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/promptcache"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/util"
)

// Store Prompt 存储接口。
type Store interface {
	CreatePrompt(ctx context.Context, p *domain.Prompt) error
	DeletePrompt(ctx context.Context, promptID string) error
	GetPrompt(ctx context.Context, promptID string) (*domain.Prompt, error)
	ListPrompts(ctx context.Context, workspaceID string) ([]*domain.Prompt, error)

	CreatePromptVersion(ctx context.Context, v *domain.PromptVersion) error
	GetPromptVersion(ctx context.Context, versionID string) (*domain.PromptVersion, error)
	GetPromptVersionByNumber(ctx context.Context, promptID string, version int) (*domain.PromptVersion, error)
	ListPromptVersions(ctx context.Context, promptID string) ([]*domain.PromptVersion, error)
}

// Service Prompt 服务。
type Service struct {
	store Store
	cache *promptcache.Cache
}

// NewService 创建 Prompt 服务。不可变版本按 versionID 缓存，无需环境失效广播。
func NewService(store Store, cache *promptcache.Cache) *Service {
	if cache == nil {
		cache = promptcache.NewCache()
	}
	return &Service{store: store, cache: cache}
}

// CreatePromptRequest 创建 Prompt 请求。
type CreatePromptRequest struct {
	WorkspaceID     string `json:"workspace_id"`
	Name            string `json:"name"`
	Category        string `json:"category"`
	Template        string `json:"template"`
	VariablesSchema string `json:"variables_schema"`
	CreatedBy       string `json:"created_by"`
}

// CreatePrompt 创建 Prompt 并产生不可变 v1。
func (s *Service) CreatePrompt(ctx context.Context, req CreatePromptRequest) (*domain.Prompt, *domain.PromptVersion, error) {
	// 先完成无副作用校验，避免模板错误留下无版本 Prompt。
	if _, err := promptcache.Compile("", req.Template, req.VariablesSchema); err != nil {
		return nil, nil, fmt.Errorf("compile: %w", err)
	}
	p := &domain.Prompt{
		ID:          util.GenerateID(),
		WorkspaceID: req.WorkspaceID,
		Name:        req.Name,
		Category:    req.Category,
		CreatedBy:   req.CreatedBy,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.store.CreatePrompt(ctx, p); err != nil {
		return nil, nil, fmt.Errorf("create prompt: %w", err)
	}
	v, err := s.CreateVersion(ctx, p.ID, req.Template, req.VariablesSchema, req.CreatedBy)
	if err != nil {
		return nil, nil, s.rollbackNewPrompt(ctx, p.ID, err)
	}
	return p, v, nil
}

func (s *Service) rollbackNewPrompt(ctx context.Context, promptID string, cause error) error {
	if err := s.store.DeletePrompt(ctx, promptID); err != nil {
		return fmt.Errorf("%w; rollback new prompt: %v", cause, err)
	}
	return cause
}

// CreateVersion 新增一个只包含模板和变量契约的 immutable 版本。
func (s *Service) CreateVersion(ctx context.Context, promptID, tmpl, schema, createdBy string) (*domain.PromptVersion, error) {
	// 编译校验：模板语法、变量 schema 合法才允许保存。
	if _, err := promptcache.Compile("", tmpl, schema); err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	if _, err := s.store.GetPrompt(ctx, promptID); err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}

	existing, err := s.store.ListPromptVersions(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	v := &domain.PromptVersion{
		ID:              util.GenerateID(),
		PromptID:        promptID,
		Version:         len(existing) + 1,
		Template:        tmpl,
		VariablesSchema: orEmptyObj(schema),
		Hash:            hashTemplate(tmpl),
		TokenEstimate:   promptcache.EstimateTokens(tmpl),
		CreatedBy:       createdBy,
		CreatedAt:       time.Now(),
	}
	if err := s.store.CreatePromptVersion(ctx, v); err != nil {
		return nil, fmt.Errorf("create version: %w", err)
	}
	return v, nil
}

// RenderByVersion 按具体版本号渲染（供 Agent 快照里 pinned 的版本使用，老对话不切版）。
func (s *Service) RenderByVersion(ctx context.Context, versionID string, vars map[string]any) (string, error) {
	if comp, ok := s.cache.Get(versionID); ok {
		return comp.Render(ctx, vars)
	}
	v, err := s.store.GetPromptVersion(ctx, versionID)
	if err != nil {
		return "", fmt.Errorf("get version: %w", err)
	}
	comp, err := promptcache.Compile(v.ID, v.Template, v.VariablesSchema)
	if err != nil {
		return "", err
	}
	s.cache.Put(versionID, comp)
	return comp.Render(ctx, vars)
}

// ListPrompts / ListVersions 透传。
func (s *Service) ListPrompts(ctx context.Context, ws string) ([]*domain.Prompt, error) {
	return s.store.ListPrompts(ctx, ws)
}

// GetPrompt 返回 Prompt 元数据，供 Agent Builder 校验 system/user 用途与工作空间。
func (s *Service) GetPrompt(ctx context.Context, promptID string) (*domain.Prompt, error) {
	return s.store.GetPrompt(ctx, promptID)
}

// EnsurePromptWorkspace 校验 Prompt 属于当前 Workspace。
func (s *Service) EnsurePromptWorkspace(ctx context.Context, promptID, workspaceID string) error {
	p, err := s.store.GetPrompt(ctx, promptID)
	if err != nil || workspaceID == "" || p.WorkspaceID != workspaceID {
		return fmt.Errorf("prompt not found")
	}
	return nil
}

// GetVersion 返回指定不可变 Prompt 版本。
func (s *Service) GetVersion(ctx context.Context, versionID string) (*domain.PromptVersion, error) {
	return s.store.GetPromptVersion(ctx, versionID)
}

func (s *Service) ListVersions(ctx context.Context, promptID string) ([]*domain.PromptVersion, error) {
	return s.store.ListPromptVersions(ctx, promptID)
}

// Diff 返回两个牌
// 这里使用无依赖的 LCS 行 diff，见 diff.go。
func (s *Service) Diff(ctx context.Context, promptID string, fromVer, toVer int) (string, error) {
	from, err := s.store.GetPromptVersionByNumber(ctx, promptID, fromVer)
	if err != nil {
		return "", fmt.Errorf("get from version: %w", err)
	}
	to, err := s.store.GetPromptVersionByNumber(ctx, promptID, toVer)
	if err != nil {
		return "", fmt.Errorf("get to version: %w", err)
	}
	return UnifiedDiff(from.Template, to.Template), nil
}

func hashTemplate(tmpl string) string {
	h := sha256.Sum256([]byte(tmpl))
	return hex.EncodeToString(h[:])
}

func orEmptyObj(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}
