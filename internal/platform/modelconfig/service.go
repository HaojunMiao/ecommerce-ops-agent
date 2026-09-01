// Package modelconfig manages immutable, workspace-scoped model configuration versions.
package modelconfig

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/llm"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/util"
)

const (
	DefaultTimeoutMS      = 120000
	DoubaoCredentialRef   = "DOUBAO_API_KEY"
	DeepSeekCredentialRef = "DEEPSEEK_API_KEY"
)

// ModelConfigVersion is the only model-control-plane entity. Every row is immutable.
// Secrets are never persisted; CredentialRef names a deployment-provided environment secret.
type ModelConfigVersion struct {
	ID                         string    `json:"id"`
	WorkspaceID                string    `json:"workspace_id"`
	Name                       string    `json:"name"`
	Version                    int       `json:"version"`
	ProviderKind               string    `json:"provider_kind"`
	BaseURL                    string    `json:"base_url"`
	ModelName                  string    `json:"model_name"`
	CredentialRef              string    `json:"credential_ref"`
	TimeoutMS                  int       `json:"timeout_ms"`
	MaxRetries                 int       `json:"max_retries"`
	InputPricePerMillion       float64   `json:"input_price_per_million"`
	OutputPricePerMillion      float64   `json:"output_price_per_million"`
	CachedInputPricePerMillion float64   `json:"cached_input_price_per_million"`
	CreatedBy                  string    `json:"created_by"`
	CreatedAt                  time.Time `json:"created_at"`
}

type Store interface {
	CreateConfigVersion(context.Context, *ModelConfigVersion) error
	ListConfigVersions(context.Context, string) ([]*ModelConfigVersion, error)
	GetConfigVersion(context.Context, string) (*ModelConfigVersion, error)
}

type Service struct {
	store Store

	credentialsMu  sync.RWMutex
	credentials    map[string]string
	endpointPolicy interface {
		ValidateURL(context.Context, string) error
	}
}

func NewService(store Store) *Service {
	return &Service{store: store, credentials: make(map[string]string)}
}

// SetCredential registers a process-local secret. It is deliberately not written to the database.
func (s *Service) SetCredential(ref, value string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return
	}
	s.credentialsMu.Lock()
	defer s.credentialsMu.Unlock()
	if value == "" {
		delete(s.credentials, ref)
		return
	}
	s.credentials[ref] = value
}

func (s *Service) ConfigureEndpointPolicy(policy interface {
	ValidateURL(context.Context, string) error
}) {
	s.endpointPolicy = policy
}

type EnsureConfigRequest struct {
	WorkspaceID                string
	Name                       string
	ProviderKind               string
	BaseURL                    string
	ModelName                  string
	CredentialRef              string
	TimeoutMS                  int
	MaxRetries                 int
	InputPricePerMillion       float64
	OutputPricePerMillion      float64
	CachedInputPricePerMillion float64
	CreatedBy                  string
}

// EnsureConfigVersion returns the current matching immutable version, or appends a new version
// when deployment-provided model settings change.
func (s *Service) EnsureConfigVersion(ctx context.Context, req EnsureConfigRequest) (*ModelConfigVersion, error) {
	normalizeRequest(&req)
	if err := s.validateRequest(ctx, req); err != nil {
		return nil, err
	}
	versions, err := s.store.ListConfigVersions(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	latestVersion := 0
	var latest *ModelConfigVersion
	for _, candidate := range versions {
		if candidate.Name != req.Name {
			continue
		}
		if candidate.Version > latestVersion {
			latestVersion = candidate.Version
			latest = candidate
		}
	}
	if latest != nil && matchesRequest(latest, req) {
		return latest, nil
	}
	v := &ModelConfigVersion{
		ID: util.GenerateID(), WorkspaceID: req.WorkspaceID, Name: req.Name, Version: latestVersion + 1,
		ProviderKind: req.ProviderKind, BaseURL: req.BaseURL, ModelName: req.ModelName,
		CredentialRef: req.CredentialRef, TimeoutMS: req.TimeoutMS, MaxRetries: req.MaxRetries,
		InputPricePerMillion: req.InputPricePerMillion, OutputPricePerMillion: req.OutputPricePerMillion,
		CachedInputPricePerMillion: req.CachedInputPricePerMillion, CreatedBy: req.CreatedBy, CreatedAt: time.Now(),
	}
	if err := s.store.CreateConfigVersion(ctx, v); err != nil {
		return nil, fmt.Errorf("create model config version: %w", err)
	}
	return v, nil
}

func normalizeRequest(req *EnsureConfigRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.ProviderKind = strings.TrimSpace(req.ProviderKind)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.ModelName = strings.TrimSpace(req.ModelName)
	req.CredentialRef = strings.TrimSpace(req.CredentialRef)
	if req.ProviderKind == "" {
		req.ProviderKind = "openai-compatible"
	}
	if req.CredentialRef == "" {
		req.CredentialRef = DoubaoCredentialRef
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = DefaultTimeoutMS
	}
	if req.CreatedBy == "" {
		req.CreatedBy = "system"
	}
}

func (s *Service) validateRequest(ctx context.Context, req EnsureConfigRequest) error {
	if req.WorkspaceID == "" || req.Name == "" || req.BaseURL == "" || req.ModelName == "" {
		return fmt.Errorf("workspace_id, name, base_url and model_name are required")
	}
	if req.MaxRetries < 0 || req.InputPricePerMillion < 0 || req.OutputPricePerMillion < 0 || req.CachedInputPricePerMillion < 0 {
		return fmt.Errorf("model retries and prices cannot be negative")
	}
	if s.endpointPolicy != nil {
		if err := s.endpointPolicy.ValidateURL(ctx, req.BaseURL); err != nil {
			return fmt.Errorf("validate model base_url: %w", err)
		}
	}
	return nil
}

func matchesRequest(v *ModelConfigVersion, req EnsureConfigRequest) bool {
	return v.ProviderKind == req.ProviderKind && v.BaseURL == req.BaseURL && v.ModelName == req.ModelName &&
		v.CredentialRef == req.CredentialRef && v.TimeoutMS == req.TimeoutMS && v.MaxRetries == req.MaxRetries &&
		v.InputPricePerMillion == req.InputPricePerMillion && v.OutputPricePerMillion == req.OutputPricePerMillion &&
		v.CachedInputPricePerMillion == req.CachedInputPricePerMillion
}

func (s *Service) ListConfigVersions(ctx context.Context, workspaceID string) ([]*ModelConfigVersion, error) {
	return s.store.ListConfigVersions(ctx, workspaceID)
}

func (s *Service) ValidateConfigVersion(ctx context.Context, workspaceID, versionID string) error {
	if versionID == "" {
		return fmt.Errorf("model_config_version_id is required")
	}
	v, err := s.store.GetConfigVersion(ctx, versionID)
	if err != nil {
		return err
	}
	if v.WorkspaceID != workspaceID {
		return fmt.Errorf("model config version belongs to another workspace")
	}
	return nil
}

func (s *Service) ResolveConfig(ctx context.Context, versionID string) (*llm.ResolvedModelConfig, error) {
	v, err := s.store.GetConfigVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	s.credentialsMu.RLock()
	apiKey := s.credentials[v.CredentialRef]
	s.credentialsMu.RUnlock()
	if apiKey == "" {
		return nil, fmt.Errorf("model credential %q is not configured", v.CredentialRef)
	}
	return &llm.ResolvedModelConfig{
		ID: v.ID, ProviderKind: v.ProviderKind, BaseURL: v.BaseURL, APIKey: apiKey, Model: v.ModelName,
		TimeoutMS: v.TimeoutMS, MaxRetries: v.MaxRetries,
		InputPricePerMillion: v.InputPricePerMillion, OutputPricePerMillion: v.OutputPricePerMillion,
		CachedInputPricePerMillion: v.CachedInputPricePerMillion,
	}, nil
}
