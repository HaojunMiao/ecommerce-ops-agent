package guard

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RuleConfig 是某个工作空间自行配置的动态规则。
// PatternOrModel 在本课只作为普通文本模式使用，尚未真正调用分类模型。
type RuleConfig struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	Kind           string `json:"kind"`
	Hook           string `json:"hook"`
	PatternOrModel string `json:"pattern_or_model"`
	Action         string `json:"action"`
	Enabled        bool   `json:"enabled"`
}

// Service 在默认规则之上叠加工作空间规则和模型调用配额。
// 第 18 课仍使用进程内存，服务重启后配置和用量会丢失。
type Service struct {
	mu       sync.RWMutex
	defaults *Pipeline
	rules    map[string]RuleConfig
	quotas   map[string]map[string]*Quota
	sequence atomic.Uint64
}

func NewService(defaults *Pipeline) *Service {
	return &Service{
		defaults: defaults,
		rules:    make(map[string]RuleConfig),
		quotas:   make(map[string]map[string]*Quota),
	}
}

type Quota struct {
	Metric string `json:"metric"`
	Period string `json:"period"`
	Used   int64  `json:"used"`
	Limit  int64  `json:"limit"`
}

// Evaluate 根据 hook 选择规则：输入/输出执行内容规则，每次模型调用消费配额；
// 随后再执行当前工作空间在相同 hook 上启用的动态规则。
func (s *Service) Evaluate(ctx context.Context, workspaceID, hook, text string) (Decision, error) {
	decision := Decision{Allowed: true, SanitizedText: text, Classification: "public"}
	if hook == "on_llm_call" {
		allowed, remaining := s.consumeQuota(workspaceID, "llm_calls", 1)
		if !allowed {
			decision.Allowed = false
			decision.Reasons = []string{"quota_exceeded:llm_calls"}
			return decision, nil
		}
		decision.Reasons = []string{fmt.Sprintf("quota_remaining:llm_calls:%d", remaining)}
	}
	if s.defaults != nil && (hook == "on_input" || hook == "on_output") {
		var err error
		decision, err = s.defaults.Evaluate(ctx, text)
		if err != nil || !decision.Allowed {
			return decision, err
		}
	}

	s.mu.RLock()
	rules := make([]RuleConfig, 0, len(s.rules))
	for _, rule := range s.rules {
		if rule.WorkspaceID == workspaceID && rule.Enabled && rule.Hook == hook {
			rules = append(rules, rule)
		}
	}
	s.mu.RUnlock()
	for _, rule := range rules {
		if rule.PatternOrModel == "" ||
			!strings.Contains(strings.ToLower(decision.SanitizedText), strings.ToLower(rule.PatternOrModel)) {
			continue
		}
		decision.Reasons = append(decision.Reasons, "dynamic:"+rule.ID)
		switch rule.Action {
		case "block":
			decision.Allowed = false
			return decision, nil
		case "redact":
			decision.SanitizedText = replaceAllFold(decision.SanitizedText, rule.PatternOrModel, "[REDACTED]")
		case "warn":
			// warn 只记录原因，不修改内容和放行结果。
		default:
			return Decision{}, fmt.Errorf("unsupported guard action %q", rule.Action)
		}
	}
	return decision, nil
}

func replaceAllFold(text, pattern, replacement string) string {
	if pattern == "" {
		return text
	}
	return regexp.MustCompile(`(?i)`+regexp.QuoteMeta(pattern)).ReplaceAllString(text, replacement)
}

func (s *Service) Create(_ context.Context, workspaceID string, rule RuleConfig) (RuleConfig, error) {
	if workspaceID == "" || rule.Kind == "" || rule.Hook == "" || rule.Action == "" {
		return RuleConfig{}, fmt.Errorf("workspace, kind, hook and action are required")
	}
	rule.ID = fmt.Sprintf("guard-rule-%d", s.sequence.Add(1))
	rule.WorkspaceID = workspaceID
	s.mu.Lock()
	s.rules[rule.ID] = rule
	s.mu.Unlock()
	return rule, nil
}

func (s *Service) Update(_ context.Context, workspaceID, id string, update RuleConfig) (RuleConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rules[id]
	if !ok || current.WorkspaceID != workspaceID {
		return RuleConfig{}, fmt.Errorf("guard rule %s not found", id)
	}
	update.ID, update.WorkspaceID = current.ID, current.WorkspaceID
	s.rules[id] = update
	return update, nil
}

func (s *Service) List(_ context.Context, workspaceID string) []RuleConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]RuleConfig, 0)
	for _, rule := range s.rules {
		if rule.WorkspaceID == workspaceID {
			result = append(result, rule)
		}
	}
	return result
}

func (s *Service) SetQuota(_ context.Context, workspaceID, metric string, limit int64) (Quota, error) {
	if workspaceID == "" || metric != "llm_calls" || limit < 0 {
		return Quota{}, fmt.Errorf("workspace, supported metric and non-negative limit are required")
	}
	period := time.Now().UTC().Format("2006-01")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quotas[workspaceID] == nil {
		s.quotas[workspaceID] = make(map[string]*Quota)
	}
	current := s.quotas[workspaceID][metric]
	if current == nil || current.Period != period {
		current = &Quota{Metric: metric, Period: period}
		s.quotas[workspaceID][metric] = current
	}
	current.Limit = limit
	return *current, nil
}

func (s *Service) ListQuotas(_ context.Context, workspaceID string) []Quota {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Quota, 0, len(s.quotas[workspaceID]))
	for _, quota := range s.quotas[workspaceID] {
		result = append(result, *quota)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Metric < result[j].Metric })
	return result
}

// consumeQuota 在同一把锁内完成检查与扣减，避免并发请求同时通过上限检查。
// 未配置的维度保持不限额，以 -1 表示没有剩余额度概念。
func (s *Service) consumeQuota(workspaceID, metric string, amount int64) (bool, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	quota := s.quotas[workspaceID][metric]
	if quota == nil {
		return true, -1
	}
	period := time.Now().UTC().Format("2006-01")
	if quota.Period != period {
		quota.Period, quota.Used = period, 0
	}
	if amount < 0 || quota.Used+amount > quota.Limit {
		return false, quota.Limit - quota.Used
	}
	quota.Used += amount
	return true, quota.Limit - quota.Used
}
