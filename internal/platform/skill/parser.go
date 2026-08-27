// Package skill 负责解析和版本化 Agent Skill。
package skill

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Package struct {
	Name                   string   `json:"name" yaml:"name"`
	Description            string   `json:"description" yaml:"description"`
	Instructions           string   `json:"instructions" yaml:"-"`
	AllowedTools           []string `json:"allowed_tools" yaml:"allowed-tools"`
	AllowedKBs             []string `json:"allowed_kbs" yaml:"allowed-kbs"`
	RequiresNetwork        bool     `json:"requires_network" yaml:"requires_network"`
	DisableModelInvocation bool     `json:"disable_model_invocation" yaml:"disable-model-invocation"`
	MaxSteps               int      `json:"max_steps" yaml:"max-steps"`
}

// ParseSkillMD 把 YAML front matter 解析为权限与运行元数据，
// 并把后面的 Markdown 正文作为模型激活 Skill 后才读取的完整指令。
func ParseSkillMD(raw []byte) (Package, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Package{}, fmt.Errorf("SKILL.md must start with YAML front matter")
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "---\n"), "\n---\n", 2)
	if len(parts) != 2 {
		return Package{}, fmt.Errorf("SKILL.md front matter is not closed")
	}
	// 兼容课程早期使用的下划线字段，新格式优先使用短横线。
	var metadata struct {
		Package            `yaml:",inline"`
		AllowedToolsLegacy []string `yaml:"allowed_tools"`
		AllowedKBsLegacy   []string `yaml:"allowed_kbs"`
		MaxStepsLegacy     int      `yaml:"max_steps"`
	}
	if err := yaml.Unmarshal([]byte(parts[0]), &metadata); err != nil {
		return Package{}, fmt.Errorf("parse skill metadata: %w", err)
	}
	pkg := metadata.Package
	if len(pkg.AllowedTools) == 0 {
		pkg.AllowedTools = metadata.AllowedToolsLegacy
	}
	if len(pkg.AllowedKBs) == 0 {
		pkg.AllowedKBs = metadata.AllowedKBsLegacy
	}
	if pkg.MaxSteps == 0 {
		pkg.MaxSteps = metadata.MaxStepsLegacy
	}
	if pkg.MaxSteps == 0 {
		pkg.MaxSteps = 8
	}
	pkg.Instructions = strings.TrimSpace(parts[1])
	if pkg.Name == "" || pkg.Description == "" || pkg.Instructions == "" {
		return Package{}, fmt.Errorf("name, description and instructions are required")
	}
	if pkg.MaxSteps <= 0 || pkg.MaxSteps > 20 {
		return Package{}, fmt.Errorf("max_steps must be between 1 and 20")
	}
	if err := validateUniqueNonEmpty(pkg.AllowedTools, "allowed tool name", "allowed tool"); err != nil {
		return Package{}, err
	}
	if err := validateUniqueNonEmpty(pkg.AllowedKBs, "allowed knowledge base id", "allowed knowledge base"); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

func validateUniqueNonEmpty(values []string, emptyLabel, duplicateLabel string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", emptyLabel)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s %q is duplicated", duplicateLabel, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
