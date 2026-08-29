// Package tooling 提供 REST 与内部 SDK 工具的统一执行抽象。
package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/xeipuuv/gojsonschema"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
)

// Executor 是所有工具执行器的统一接口。
type Executor interface {
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// BuiltTool 把"给模型看的描述"与"实际执行器"打包在一起，供 Runtime Engine 使用。
type BuiltTool struct {
	Name            string
	Info            *schema.ToolInfo
	Executor        Executor
	Tool            einotool.InvokableTool // Eino ToolsNode/ADK 的标准执行入口
	Sensitive       bool                   // 敏感工具调用前需人在环审批
	RequiresNetwork bool                   // REST 工具受 Agent 网络策略约束
	KBScoped        bool                   // search_knowledge_base 受 Agent/Skill KB allowlist 约束
	ApprovalUI      ApprovalPresentation   // 敏感操作固定审批卡片的展示元数据
}

// ApprovalPresentation 只描述审批卡片中的业务文案，不携带任何动态 UI 协议。
type ApprovalPresentation struct {
	Title          string            `json:"title,omitempty"`
	OperationLabel string            `json:"operation_label,omitempty"`
	RiskLabel      string            `json:"risk_label,omitempty"`
	FieldLabels    map[string]string `json:"field_labels,omitempty"`
	FieldOrder     []string          `json:"field_order,omitempty"`
	CurrencyFields map[string]string `json:"currency_fields,omitempty"`
}

// InvokableTool 使用给定的 ToolInfo 构造 Eino 标准工具。传 nil 时沿用注册表中的描述。
// Runtime 可借此按 Agent/Skill 的知识库授权收窄参数枚举，同时复用同一个执行器。
func (b *BuiltTool) InvokableTool(info *schema.ToolInfo) einotool.InvokableTool {
	if info == nil {
		info = b.Info
	}
	return &executorInvokableTool{info: info, executor: b.Executor}
}

type executorInvokableTool struct {
	info     *schema.ToolInfo
	executor Executor
}

func (t *executorInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *executorInvokableTool) InvokableRun(ctx context.Context, arguments string, _ ...einotool.Option) (string, error) {
	return t.executor.Execute(ctx, json.RawMessage(arguments))
}

// Registry 按 toolID 解析配置并构造执行器。
type Registry struct {
	tools *tool.Service
	sdk   map[string]sdkEntry // internal_sdk: name -> 执行器 + 描述
}

type sdkEntry struct {
	exec     Executor
	desc     string
	paramsJS string // JSON Schema（可空）
}

// NewRegistry 创建工具注册表。
func NewRegistry(tools *tool.Service) *Registry {
	return &Registry{
		tools: tools,
		sdk:   make(map[string]sdkEntry),
	}
}

// RegisterSDK 注册一个 internal_sdk 工具（如 KB 检索）。name 必须与 Tool Registry
// 中该工具的 endpoint_config.sdk_name 对应。
func (r *Registry) RegisterSDK(name, desc, paramsJSONSchema string, exec Executor) {
	r.sdk[name] = sdkEntry{exec: exec, desc: desc, paramsJS: paramsJSONSchema}
}

// Build 根据 toolID 取出已发布的配置，构造一个可执行 + 可描述的 BuiltTool。
func (r *Registry) Build(ctx context.Context, toolID string) (*BuiltTool, error) {
	cfg, err := r.tools.GetToolConfig(ctx, toolID)
	if err != nil {
		return nil, fmt.Errorf("get tool config: %w", err)
	}

	t, err := r.tools.GetToolMeta(ctx, toolID)
	if err != nil {
		return nil, fmt.Errorf("get tool meta: %w", err)
	}

	exec, paramsJS, err := r.buildExecutor(cfg)
	if err != nil {
		return nil, err
	}
	exec, err = withSchemaValidation(exec, paramsJS)
	if err != nil {
		return nil, fmt.Errorf("compile tool input schema: %w", err)
	}

	info, err := buildToolInfo(t.Name, t.Description, paramsJS)
	if err != nil {
		return nil, fmt.Errorf("build tool info: %w", err)
	}

	built := &BuiltTool{
		Name: t.Name, Info: info, Executor: exec, Sensitive: t.Sensitive,
		RequiresNetwork: sourceRequiresNetwork(cfg.SourceType),
		KBScoped:        isKBScopedTool(cfg),
		ApprovalUI:      approvalPresentation(cfg.Schema),
	}
	built.Tool = built.InvokableTool(nil)
	return built, nil
}

// BuildByVersion 按【工具版本 ID】构造执行器:用于 Agent 快照里 pin 死的工具版本。
//
// 与 Build(取 current)相对——这是不可变快照的执行入口:老对话即使工具发了新版,
// 也仍按它创建时 pin 的那个版本执行。Build 仍保留给"试调当前草稿版本"用。
func (r *Registry) BuildByVersion(ctx context.Context, toolVersionID string) (*BuiltTool, error) {
	cfg, toolID, err := r.tools.GetToolConfigByVersion(ctx, toolVersionID)
	if err != nil {
		return nil, fmt.Errorf("get tool config by version: %w", err)
	}

	t, err := r.tools.GetToolMeta(ctx, toolID)
	if err != nil {
		return nil, fmt.Errorf("get tool meta: %w", err)
	}

	exec, paramsJS, err := r.buildExecutor(cfg)
	if err != nil {
		return nil, err
	}
	exec, err = withSchemaValidation(exec, paramsJS)
	if err != nil {
		return nil, fmt.Errorf("compile tool input schema: %w", err)
	}

	info, err := buildToolInfo(t.Name, t.Description, paramsJS)
	if err != nil {
		return nil, fmt.Errorf("build tool info: %w", err)
	}

	built := &BuiltTool{
		Name: t.Name, Info: info, Executor: exec, Sensitive: t.Sensitive,
		RequiresNetwork: sourceRequiresNetwork(cfg.SourceType),
		KBScoped:        isKBScopedTool(cfg),
		ApprovalUI:      approvalPresentation(cfg.Schema),
	}
	built.Tool = built.InvokableTool(nil)
	return built, nil
}

func approvalPresentation(schema map[string]any) ApprovalPresentation {
	raw, _ := schema["x-kbot-approval"].(map[string]any)
	p := ApprovalPresentation{
		FieldLabels: make(map[string]string), CurrencyFields: make(map[string]string),
	}
	p.Title, _ = raw["title"].(string)
	p.OperationLabel, _ = raw["operation_label"].(string)
	p.RiskLabel, _ = raw["risk_label"].(string)
	if fields, ok := raw["field_labels"].(map[string]any); ok {
		for name, value := range fields {
			if label, ok := value.(string); ok {
				p.FieldLabels[name] = label
			}
		}
	}
	if order, ok := raw["field_order"].([]any); ok {
		for _, value := range order {
			if name, ok := value.(string); ok {
				p.FieldOrder = append(p.FieldOrder, name)
			}
		}
	}
	if currencies, ok := raw["currency_fields"].(map[string]any); ok {
		for name, value := range currencies {
			if symbol, ok := value.(string); ok {
				p.CurrencyFields[name] = symbol
			}
		}
	}
	return p
}

func sourceRequiresNetwork(sourceType string) bool {
	switch sourceType {
	case "rest_api":
		return true
	default:
		return false
	}
}

func isKBScopedTool(cfg *tool.ToolConfig) bool {
	if cfg.SourceType != "internal_sdk" {
		return false
	}
	name, _ := cfg.EndpointConfig["sdk_name"].(string)
	return name == "search_knowledge_base"
}

// buildExecutor 是 source_type → Factory 的路由（§14.2 的 factories map）。
// 返回执行器和用于描述参数的 JSON Schema 字符串。
func (r *Registry) buildExecutor(cfg *tool.ToolConfig) (Executor, string, error) {
	schemaJSON := marshalOrEmpty(cfg.Schema)
	httpClient := r.tools.HTTPClient(30 * time.Second)
	switch cfg.SourceType {
	case "rest_api":
		return newRESTExecutor(httpClient, cfg), schemaJSON, nil
	case "internal_sdk":
		name, _ := cfg.EndpointConfig["sdk_name"].(string)
		entry, ok := r.sdk[name]
		if !ok {
			return nil, "", fmt.Errorf("internal_sdk %q not registered", name)
		}
		js := entry.paramsJS
		if js == "" {
			js = schemaJSON
		}
		return entry.exec, js, nil
	default:
		return nil, "", fmt.Errorf("unknown source_type: %s", cfg.SourceType)
	}
}

// buildToolInfo 把工具的参数 JSON Schema 转成 Eino 的 ToolInfo。
func buildToolInfo(name, desc, paramsJSONSchema string) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: name, Desc: desc}
	if paramsJSONSchema == "" || paramsJSONSchema == "{}" {
		return info, nil
	}
	js := &jsonschema.Schema{}
	if err := json.Unmarshal([]byte(paramsJSONSchema), js); err != nil {
		return nil, fmt.Errorf("unmarshal params schema: %w", err)
	}
	info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
	return info, nil
}

func marshalOrEmpty(m map[string]interface{}) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

type schemaValidatingExecutor struct {
	next   Executor
	schema *gojsonschema.Schema
}

func withSchemaValidation(next Executor, schemaJSON string) (Executor, error) {
	if next == nil || schemaJSON == "" || schemaJSON == "{}" {
		return next, nil
	}
	compiled, err := gojsonschema.NewSchema(gojsonschema.NewStringLoader(schemaJSON))
	if err != nil {
		return nil, err
	}
	return &schemaValidatingExecutor{next: next, schema: compiled}, nil
}

func (e *schemaValidatingExecutor) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if len(args) == 0 || !json.Valid(args) {
		return "", fmt.Errorf("tool input must be valid JSON")
	}
	result, err := e.schema.Validate(gojsonschema.NewBytesLoader(args))
	if err != nil {
		return "", fmt.Errorf("validate tool input: %w", err)
	}
	if !result.Valid() {
		problems := make([]string, 0, len(result.Errors()))
		for _, item := range result.Errors() {
			problems = append(problems, item.String())
		}
		return "", fmt.Errorf("tool input violates schema: %s", strings.Join(problems, "; "))
	}
	return e.next.Execute(ctx, args)
}

// authHeaders 解析 Tool 的通用 Header 鉴权配置。
// 支持 {"header":"Authorization","value":"Bearer ..."} 与 {"headers":{"X-Key":"..."}}。
func authHeaders(auth map[string]interface{}) map[string]string {
	headers := make(map[string]string)
	if h, ok := auth["header"].(string); ok && h != "" {
		if v, ok := auth["value"].(string); ok {
			headers[h] = v
		}
	}
	if raw, ok := auth["headers"].(map[string]interface{}); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok && k != "" {
				headers[k] = s
			}
		}
	}
	return headers
}
