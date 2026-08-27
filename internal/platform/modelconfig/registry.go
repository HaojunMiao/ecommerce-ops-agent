// Package modelconfig 管理模型部署配置版本。
package modelconfig

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
)

// Deployment 描述一个可实际调用的模型部署。APIKey 只在发布和解析时短暂出现，
// 列表接口只暴露 HasAPIKey，避免把明文凭据返回管理端。
type Deployment struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	BaseURL    string `json:"base_url"`
	MaxRetries int    `json:"max_retries"`
	APIKey     string `json:"-"`
	HasAPIKey  bool   `json:"has_api_key"`

	apiKeyCiphertext []byte
}

// ProfileVersion 是不可变的模型调用方案，Deployments 按顺序表示主部署和备用部署。
type ProfileVersion struct {
	ID                string       `json:"id"`
	WorkspaceID       string       `json:"workspace_id"`
	Name              string       `json:"name"`
	ClassificationMax string       `json:"classification_max"`
	Deployments       []Deployment `json:"deployments"`
}

type Registry struct {
	mu       sync.RWMutex
	profiles map[string]ProfileVersion
	aead     cipher.AEAD
}

// NewRegistry 使用传入密钥的 SHA-256 结果构造 AES-GCM；未传入时生成进程内随机密钥。
func NewRegistry(credentialKeys ...[]byte) *Registry {
	key := make([]byte, 32)
	if len(credentialKeys) > 0 && len(credentialKeys[0]) > 0 {
		digest := sha256.Sum256(credentialKeys[0])
		copy(key, digest[:])
	} else if _, err := io.ReadFull(rand.Reader, key); err != nil {
		panic(fmt.Errorf("generate model credential key: %w", err))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &Registry{profiles: make(map[string]ProfileVersion), aead: aead}
}

// Publish 校验并发布新版本。同一 ID 不能被覆盖，从而保证已固定的历史运行不漂移。
func (r *Registry) Publish(_ context.Context, profile ProfileVersion) error {
	if profile.ID == "" || profile.WorkspaceID == "" || len(profile.Deployments) == 0 {
		return fmt.Errorf("id, workspace and deployments are required")
	}
	if !validClassification(profile.ClassificationMax) {
		return fmt.Errorf("invalid classification %q", profile.ClassificationMax)
	}
	profile.Deployments = append([]Deployment(nil), profile.Deployments...)
	for index := range profile.Deployments {
		deployment := &profile.Deployments[index]
		parsed, err := url.Parse(deployment.BaseURL)
		if deployment.Provider == "" || deployment.Model == "" || err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("invalid deployment")
		}
		ciphertext, err := r.encrypt(deployment.APIKey)
		if err != nil {
			return fmt.Errorf("encrypt deployment credentials: %w", err)
		}
		deployment.HasAPIKey = len(ciphertext) > 0
		deployment.APIKey = ""
		deployment.apiKeyCiphertext = ciphertext
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.profiles[profile.ID]; exists {
		return fmt.Errorf("model profile %s already exists", profile.ID)
	}
	r.profiles[profile.ID] = profile
	return nil
}

func (r *Registry) Validate(ctx context.Context, workspaceID, versionID string) error {
	_, err := r.Resolve(ctx, workspaceID, versionID)
	return err
}

// Resolve 仅解析当前工作空间的版本，并在运行时解密调用凭据。
func (r *Registry) Resolve(_ context.Context, workspaceID, versionID string) (ProfileVersion, error) {
	r.mu.RLock()
	profile, ok := r.profiles[versionID]
	r.mu.RUnlock()
	if !ok || profile.WorkspaceID != workspaceID {
		return ProfileVersion{}, fmt.Errorf("model profile %s not found", versionID)
	}
	profile.Deployments = append([]Deployment(nil), profile.Deployments...)
	for index := range profile.Deployments {
		plaintext, err := r.decrypt(profile.Deployments[index].apiKeyCiphertext)
		if err != nil {
			return ProfileVersion{}, fmt.Errorf("decrypt deployment credentials: %w", err)
		}
		profile.Deployments[index].APIKey = plaintext
		profile.Deployments[index].apiKeyCiphertext = nil
	}
	return profile, nil
}

// List 只返回当前工作空间的版本，并显式移除所有凭据内容。
func (r *Registry) List(_ context.Context, workspaceID string) []ProfileVersion {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ProfileVersion, 0, len(r.profiles))
	for _, profile := range r.profiles {
		if profile.WorkspaceID == workspaceID {
			profile.Deployments = append([]Deployment(nil), profile.Deployments...)
			for index := range profile.Deployments {
				profile.Deployments[index].APIKey = ""
				profile.Deployments[index].apiKeyCiphertext = nil
			}
			result = append(result, profile)
		}
	}
	return result
}

func (r *Registry) encrypt(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	nonce := make([]byte, r.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return r.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (r *Registry) decrypt(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	nonceSize := r.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("invalid API key ciphertext")
	}
	plaintext, err := r.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func validClassification(value string) bool {
	switch strings.ToLower(value) {
	case "public", "internal", "confidential", "secret":
		return true
	default:
		return false
	}
}
