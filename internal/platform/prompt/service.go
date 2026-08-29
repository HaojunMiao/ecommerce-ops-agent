// Package prompt 管理不可变提示词版本及 dev/staging/prod 环境指针。
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

// 环境常量。
const (
	EnvDev     = "dev"
	EnvStaging = "staging"
	EnvProd    = "prod"
)

// Publisher 把"某 prompt@env 指针变了"广播出去（Redis Pub/Sub）。
type Publisher interface {
	Publish(ctx context.Context, channel, message string) error
}

// NoopPublisher 是无 Redis 时的空实现（测试 / 单进程）。
type NoopPublisher struct{}

func (NoopPublisher) Publish(context.Context, string, string) error { return nil }

// InvalidateChannel 返回某 prompt@env 的失效频道名。
func InvalidateChannel(promptID, env string) string {
	return fmt.Sprintf("prompt:%s:%s:invalidate", promptID, env)
}

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

	SetEnvBinding(ctx context.Context, promptID, env, versionID string) error
	GetEnvBinding(ctx context.Context, promptID, env string) (string, error) // 返回 versionID

}

type ModelConfigValidator interface {
	ValidateConfigVersion(ctx context.Context, workspaceID, versionID string) error
}

// Service Prompt 服务。
type Service struct {
	store  Store
	cache  *promptcache.Cache
	pub    Publisher
	models ModelConfigValidator
}

// NewService 创建 Prompt 服务。cache/pub 可由调用方共享（Runtime 与 Platform 同一份 cache）。
func NewService(store Store, cache *promptcache.Cache, pub Publisher) *Service {
	if cache == nil {
		cache = promptcache.NewCache()
	}
	if pub == nil {
		pub = NoopPublisher{}
	}
	return &Service{store: store, cache: cache, pub: pub}
}

func (s *Service) WithModelConfigs(models ModelConfigValidator) *Service {
	s.models = models
	return s
}

// Cache 暴露底层缓存（供 Runtime subscriber 重拉时写入）。
func (s *Service) Cache() *promptcache.Cache { return s.cache }

// CreatePromptRequest 创建 Prompt 请求。
type CreatePromptRequest struct {
	WorkspaceID          string                  `json:"workspace_id"`
	Name                 string                  `json:"name"`
	Category             string                  `json:"category"`
	Template             string                  `json:"template"`
	VariablesSchema      string                  `json:"variables_schema"`
	ModelConfigVersionID string                  `json:"model_config_version_id"`
	GenerationConfig     domain.GenerationConfig `json:"generation_config"`
	CreatedBy            string                  `json:"created_by"`
}

// CreatePrompt 创建 Prompt 并产生 v1，默认绑定到 dev 环境。
func (s *Service) CreatePrompt(ctx context.Context, req CreatePromptRequest) (*domain.Prompt, *domain.PromptVersion, error) {
	// Prompt 主记录、v1 和 dev 指针组成一个逻辑原子操作。先完成所有
	// 无副作用校验，避免模板或模型配置错误留下无版本 Prompt。
	if _, err := promptcache.Compile("", req.Template, req.VariablesSchema); err != nil {
		return nil, nil, fmt.Errorf("compile: %w", err)
	}
	if req.ModelConfigVersionID == "" {
		return nil, nil, fmt.Errorf("model_config_version_id is required")
	}
	if s.models != nil {
		if err := s.models.ValidateConfigVersion(ctx, req.WorkspaceID, req.ModelConfigVersionID); err != nil {
			return nil, nil, fmt.Errorf("validate model config: %w", err)
		}
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
	v, err := s.CreateVersionConfigured(ctx, p.ID, req.Template, req.VariablesSchema,
		req.ModelConfigVersionID, req.GenerationConfig, req.CreatedBy)
	if err != nil {
		return nil, nil, s.rollbackNewPrompt(ctx, p.ID, err)
	}
	// 新 Prompt 默认在 dev 生效。
	if err := s.Promote(ctx, p.ID, EnvDev, v.ID); err != nil {
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

// CreateVersion 新增一个 immutable 版本，并继承最新版本的模型与生成配置。
// 这个便捷入口不允许意外清空模型绑定。
func (s *Service) CreateVersion(ctx context.Context, promptID, tmpl, schema, createdBy string) (*domain.PromptVersion, error) {
	existing, err := s.store.ListPromptVersions(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	if len(existing) == 0 {
		return nil, fmt.Errorf("prompt has no version to inherit model config from")
	}
	latest := existing[0]
	for _, candidate := range existing[1:] {
		if candidate.Version > latest.Version {
			latest = candidate
		}
	}
	return s.CreateVersionConfigured(ctx, promptID, tmpl, schema, latest.ModelConfigVersionID, latest.GenerationConfig, createdBy)
}

// CreateVersionConfigured 把 Prompt、模型配置版本与生成参数作为一个原子不可变版本保存。
func (s *Service) CreateVersionConfigured(
	ctx context.Context,
	promptID, tmpl, schema, modelConfigVersionID string,
	generationConfig domain.GenerationConfig,
	createdBy string,
) (*domain.PromptVersion, error) {
	// 编译校验：模板语法、变量 schema 合法才允许保存。
	if _, err := promptcache.Compile("", tmpl, schema); err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	if modelConfigVersionID == "" {
		return nil, fmt.Errorf("model_config_version_id is required")
	}
	p, err := s.store.GetPrompt(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	if s.models != nil {
		if err := s.models.ValidateConfigVersion(ctx, p.WorkspaceID, modelConfigVersionID); err != nil {
			return nil, fmt.Errorf("validate model config: %w", err)
		}
	}

	existing, err := s.store.ListPromptVersions(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	v := &domain.PromptVersion{
		ID:                   util.GenerateID(),
		PromptID:             promptID,
		Version:              len(existing) + 1,
		Template:             tmpl,
		VariablesSchema:      orEmptyObj(schema),
		ModelConfigVersionID: modelConfigVersionID,
		GenerationConfig:     generationConfig,
		Hash:                 hashTemplate(tmpl),
		TokenEstimate:        promptcache.EstimateTokens(tmpl),
		CreatedBy:            createdBy,
		CreatedAt:            time.Now(),
	}
	if err := s.store.CreatePromptVersion(ctx, v); err != nil {
		return nil, fmt.Errorf("create version: %w", err)
	}
	return v, nil
}

// Promote 把 env 指针指向某版本（dev→staging→prod 晋升 / 首次发布都走这里）。
// 顺序：改指针 → 重编译并写本地缓存（in-process 立刻生效）→ Pub/Sub 广播（跨进程）。
func (s *Service) Promote(ctx context.Context, promptID, env, versionID string) error {
	v, err := s.store.GetPromptVersion(ctx, versionID)
	if err != nil {
		return fmt.Errorf("get version: %w", err)
	}
	if v.PromptID != promptID {
		return fmt.Errorf("version %s does not belong to prompt %s", versionID, promptID)
	}
	if err := s.store.SetEnvBinding(ctx, promptID, env, versionID); err != nil {
		return fmt.Errorf("set env binding: %w", err)
	}

	// 重。
	comp, err := promptcache.Compile(v.ID, v.Template, v.VariablesSchema)
	if err != nil {
		return fmt.Errorf("compile on promote: %w", err)
	}
	s.cache.Put(promptID, env, comp)

	// 广播失效，供其它 Runtime 进程异步重拉。
	_ = s.pub.Publish(ctx, InvalidateChannel(promptID, env), versionID)
	return nil
}

// Rollback 回滚 = 把 env 指针指回旧版本（与 Promote 同机制）。
func (s *Service) Rollback(ctx context.Context, promptID, env, versionID string) error {
	return s.Promote(ctx, promptID, env, versionID)
}

// RefreshCache 按当前 env 指针重新编译并写入本地缓存。供 Pub/Sub 订阅端收到失效
// 通知后。
func (s *Service) RefreshCache(ctx context.Context, promptID, env string) error {
	versionID, err := s.store.GetEnvBinding(ctx, promptID, env)
	if err != nil {
		return err
	}
	v, err := s.store.GetPromptVersion(ctx, versionID)
	if err != nil {
		return err
	}
	comp, err := promptcache.Compile(v.ID, v.Template, v.VariablesSchema)
	if err != nil {
		return err
	}
	s.cache.Put(promptID, env, comp)
	return nil
}

// ResolveVersion 按环境指针解析固定版本。
func (s *Service) ResolveVersion(ctx context.Context, promptID, env, _ string) (string, error) {
	return s.store.GetEnvBinding(ctx, promptID, env)
}

type ResolvedConfig struct {
	VersionID            string
	Rendered             string
	ModelConfigVersionID string
	GenerationConfig     domain.GenerationConfig
}

func (s *Service) ResolveConfig(ctx context.Context, promptID, env, userID string, vars map[string]any) (*ResolvedConfig, error) {
	versionID, err := s.store.GetEnvBinding(ctx, promptID, env)
	if err != nil {
		return nil, err
	}
	return s.ResolveConfigByVersion(ctx, versionID, vars)
}

func (s *Service) ResolveConfigByVersion(ctx context.Context, versionID string, vars map[string]any) (*ResolvedConfig, error) {
	v, err := s.store.GetPromptVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	rendered, err := s.RenderByVersion(ctx, versionID, vars)
	if err != nil {
		return nil, err
	}
	return &ResolvedConfig{
		VersionID: v.ID, Rendered: rendered, ModelConfigVersionID: v.ModelConfigVersionID,
		GenerationConfig: v.GenerationConfig,
	}, nil
}

// Render 解析版本 → 取编译产物（缓存未命中则即时编译）→ 渲染。
func (s *Service) Render(ctx context.Context, promptID, env, userID string, vars map[string]any) (string, error) {
	versionID, err := s.ResolveVersion(ctx, promptID, env, userID)
	if err != nil {
		return "", fmt.Errorf("resolve version: %w", err)
	}
	// 环境绑定的固定版本命中缓存时，直接渲染。
	if comp, ok := s.cache.Get(promptID, env); ok && comp.VersionID == versionID {
		return comp.Render(ctx, vars)
	}
	return s.RenderByVersion(ctx, versionID, vars)
}

// RenderByVersion 按具体版本号渲染（供 Agent 快照里 pinned 的版本使用，老对话不切版）。
func (s *Service) RenderByVersion(ctx context.Context, versionID string, vars map[string]any) (string, error) {
	v, err := s.store.GetPromptVersion(ctx, versionID)
	if err != nil {
		return "", fmt.Errorf("get version: %w", err)
	}
	comp, err := promptcache.Compile(v.ID, v.Template, v.VariablesSchema)
	if err != nil {
		return "", err
	}
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
