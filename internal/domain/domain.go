// Package domain 定义 kbot 的领域类型（只放数据，不含逻辑）。
package domain

import (
	"time"
)

// === 身份与组织 ===

// User 用户实体
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`   // admin / member
	PasswordHash string    `json:"-"`      // 不序列化
	Status       string    `json:"status"` // active / inactive / suspended
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// WorkspaceMember 描述用户在工作空间内的角色。
type WorkspaceMember struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Email       string    `json:"email,omitempty"`
	Name        string    `json:"name,omitempty"`
	Role        string    `json:"role"` // owner / admin / editor / member / viewer
	CreatedAt   time.Time `json:"created_at"`
}

// Workspace 工作空间实体（团队/部门分组）
type Workspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ParentID    *string   `json:"parent_id,omitempty"` // 支持嵌套
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// === Prompt管理 ===

// Prompt 提示词实体
type Prompt struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PromptVersion 提示词版本实体（immutable）
type PromptVersion struct {
	ID              string    `json:"id"`
	PromptID        string    `json:"prompt_id"`
	Version         int       `json:"version"`
	Template        string    `json:"template"`
	VariablesSchema string    `json:"variables_schema"` // JSON Schema
	Hash            string    `json:"hash"`
	TokenEstimate   int       `json:"token_estimate"` // 保存时估算并持久化
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}

// GenerationConfig 由 AgentVersion 固化，描述该 Agent 如何调用模型。
// 指针用于区分“未设置”与显式零值。
type GenerationConfig struct {
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"top_p,omitempty"`
	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Stop            []string `json:"stop,omitempty"`
	Seed            *int     `json:"seed,omitempty"`
}

// === Agent ===

// Agent Agent实体
type Agent struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Template    string    `json:"template"` // customer_support / internal_copilot 等
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AgentVersion Agent版本实体（immutable快照）
type AgentVersion struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	Version      int       `json:"version"`
	SnapshotJSON string    `json:"snapshot_json"` // 完整配置快照
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// === 会话与消息 ===

// Conversation 对话实体（原Session重命名）
type Conversation struct {
	ID                string    `json:"id"`
	AgentID           string    `json:"agent_id"`
	AgentVersionID    string    `json:"agent_version_id"`
	WorkspaceID       string    `json:"workspace_id"`
	UserID            string    `json:"user_id"`
	Status            string    `json:"status"`         // active / completed / error
	Classification    string    `json:"classification"` // public / internal / confidential / secret
	RuntimeConfigJSON string    `json:"runtime_config_json,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ConversationRuntimeConfig 只保存会话自身的来源、观测和首轮输入快照。
// Prompt、模型与生成参数的唯一真相源是 Conversation 固定的 AgentVersion。
type ConversationRuntimeConfig struct {
	Environment         string         `json:"environment,omitempty"`
	LatestTraceID       string         `json:"latest_trace_id,omitempty"`
	UserPromptVersionID string         `json:"user_prompt_version_id,omitempty"`
	UserPromptVariables map[string]any `json:"user_prompt_variables,omitempty"`
	RenderedUserPrompt  string         `json:"rendered_user_prompt,omitempty"`
}

// Message 消息实体
type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Role           string     `json:"role"` // system / user / assistant / tool
	Content        string     `json:"content"`
	ToolCalls      []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID     *string    `json:"tool_call_id,omitempty"` // 工具返回消息的关联ID
	CreatedAt      time.Time  `json:"created_at"`
}

// ToolCall 工具调用记录
type ToolCall struct {
	ID            string    `json:"id"`
	MessageID     string    `json:"message_id"`
	ToolID        string    `json:"tool_id"`
	ToolVersionID string    `json:"tool_version_id"`
	Args          string    `json:"args"` // JSON
	Result        *string   `json:"result,omitempty"`
	Error         *string   `json:"error,omitempty"`
	LatencyMs     int       `json:"latency_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

// === 数据分级（§4.6 / §7）===

// Classification 数据分级。顺序：public < internal < confidential < secret。
type Classification string

const (
	ClassPublic       Classification = "public"
	ClassInternal     Classification = "internal"
	ClassConfidential Classification = "confidential"
	ClassSecret       Classification = "secret"
)

// Level 返回分级的数值序（越高越敏感），便于比较。
func (c Classification) Level() int {
	switch c {
	case ClassPublic:
		return 0
	case ClassInternal:
		return 1
	case ClassConfidential:
		return 2
	case ClassSecret:
		return 3
	default:
		return 1 // 默认 internal
	}
}

// GreaterThan 报告 c 是否比 other 更敏感。
func (c Classification) GreaterThan(other Classification) bool {
	return c.Level() > other.Level()
}

// === Eval 门禁（§4.12）===

// EvalDataset 评估集。
type EvalDataset struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	TargetKind  string    `json:"target_kind"` // prompt / skill / agent
	CreatedAt   time.Time `json:"created_at"`
}

// EvalCase 评估用例。
type EvalCase struct {
	ID        string    `json:"id"`
	DatasetID string    `json:"dataset_id"`
	Input     string    `json:"input"`
	Expected  string    `json:"expected"` // 期望关键词 / 期望答案（按 Judge 解释）
	Metadata  string    `json:"metadata"` // JSON：来源、标签等
	CreatedAt time.Time `json:"created_at"`
}

// EvalRun 一次评估运行。
type EvalRun struct {
	ID         string     `json:"id"`
	DatasetID  string     `json:"dataset_id"`
	TargetID   string     `json:"target_id"`
	JudgeID    string     `json:"judge_id"`
	Status     string     `json:"status"` // running / passed / failed
	PassRate   float64    `json:"pass_rate"`
	Threshold  float64    `json:"threshold"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// EvalScore 单用例得分（可多维度）。
type EvalScore struct {
	RunID     string  `json:"run_id"`
	CaseID    string  `json:"case_id"`
	Dimension string  `json:"dimension"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
}

// Approval 人在环审批请求（敏感工具调用）。
type Approval struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`
	Payload    string    `json:"payload"`
	Status     string    `json:"status"` // pending / approved / rejected
	ApproverID string    `json:"approver_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// === 审计 ===

// AuditLog 审计日志实体
type AuditLog struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Actor        string    `json:"actor"` // user_id或system
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	BeforeJSON   *string   `json:"before_json,omitempty"`
	AfterJSON    *string   `json:"after_json,omitempty"`
	IP           *string   `json:"ip,omitempty"`
	UserAgent    *string   `json:"user_agent,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// === Tool Registry ===

// Tool 工具实体
type Tool struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	SourceType  string    `json:"source_type"` // rest_api / internal_sdk
	Description string    `json:"description"`
	Sensitive   bool      `json:"sensitive"` // 敏感工具调用前需人在环审批
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToolVersion 工具版本实体（immutable）
type ToolVersion struct {
	ID                  string    `json:"id"`
	ToolID              string    `json:"tool_id"`
	Version             int       `json:"version"`
	SchemaJSON          string    `json:"schema_json"`     // JSON Schema for parameters
	EndpointConfig      string    `json:"endpoint_config"` // REST URL 或内部实现配置
	AuthConfig          string    `json:"auth_config"`     // API 只返回空对象；明文仅在服务内部短暂存在
	AuthConfigEncrypted []byte    `json:"-"`
	HasAuth             bool      `json:"has_auth"`
	RetryPolicy         string    `json:"retry_policy"` // 重试策略
	Status              string    `json:"status"`       // draft / published / deprecated
	CreatedBy           string    `json:"created_by"`
	CreatedAt           time.Time `json:"created_at"`
}

// ToolTestRun 工具测试执行记录
type ToolTestRun struct {
	ID            string    `json:"id"`
	ToolID        string    `json:"tool_id"`
	ToolVersionID string    `json:"tool_version_id"`
	Input         string    `json:"input"`  // JSON
	Output        string    `json:"output"` // 结果
	Status        string    `json:"status"` // success / error
	LatencyMs     int       `json:"latency_ms"`
	Error         *string   `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type ToolInvocation struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspace_id"`
	ConversationID string    `json:"conversation_id"`
	ToolCallID     string    `json:"tool_call_id"`
	ToolVersionID  string    `json:"tool_version_id"`
	Args           string    `json:"args"`
	Result         string    `json:"result"`
	Status         string    `json:"status"`
	LatencyMS      int       `json:"latency_ms"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// === Skills ===

// Skill 技能实体（名字与元数据）。
type Skill struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SkillVersion 技能版本（immutable，存 SKILL.md 的 frontmatter + body）。
type SkillVersion struct {
	ID              string    `json:"id"`
	SkillID         string    `json:"skill_id"`
	Version         int       `json:"version"`
	FrontmatterJSON string    `json:"frontmatter_json"` // 解析后的 frontmatter
	BodyMD          string    `json:"body_md"`          // Markdown 流程正文
	Status          string    `json:"status"`           // draft / published
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}

// SkillSubscription 技能订阅（订阅到 agent 或 workspace）。
type SkillSubscription struct {
	SkillID     string    `json:"skill_id"`
	VersionID   string    `json:"version_id"`
	AgentID     string    `json:"agent_id,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// === Knowledge Base ===

// KnowledgeBase 知识库实体
type KnowledgeBase struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	Name           string `json:"name"`
	ChunkingConfig string `json:"chunking_config"` // JSON配置
	// EmbeddingModel 保存完整的嵌入器身份（供应商地址、模型和维度），而不只是模型名。
	// 这样同维度但不在同一向量空间的模型也不会被误认为兼容。
	EmbeddingModel string    `json:"embedding_model"`
	Status         string    `json:"status"` // active / indexing / error
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// KbDocument 知识库文档
type KbDocument struct {
	ID         string `json:"id"`
	KbID       string `json:"kb_id"`
	SourceType string `json:"source_type"` // upload / connector
	SourceURI  string `json:"source_uri"`
	Hash       string `json:"hash"`
	// EmbeddingIdentity 是该文档当前期望/已完成索引使用的嵌入器身份。
	// pending 时表示目标身份，processed 时表示 chunks 已在该身份下完成重建。
	EmbeddingIdentity string     `json:"embedding_identity"`
	Classification    string     `json:"classification"` // public / internal / confidential / secret
	Status            string     `json:"status"`         // pending / processed / error
	IngestedAt        *time.Time `json:"ingested_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// KbIngestJob KB ingest任务
type KbIngestJob struct {
	ID         string     `json:"id"`
	KbID       string     `json:"kb_id"`
	DocID      string     `json:"doc_id"`
	Stage      string     `json:"stage"` // parse / chunk / embed / index / done
	Retries    int        `json:"retries"`
	Error      *string    `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// ConnectorInstance Connector实例
type ConnectorInstance struct {
	ID            string     `json:"id"`
	KbID          string     `json:"kb_id"`
	ConnectorKind string     `json:"connector_kind"` // markdown_folder / confluence / etc
	ConfigJSON    string     `json:"config_json"`
	Cursor        string     `json:"cursor"` // 增量同步游标
	LastSyncAt    *time.Time `json:"last_sync_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
