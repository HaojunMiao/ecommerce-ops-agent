package domain

import "time"

// 核心领域对象
// 控制面 和 运行时 共享的一些核心数据

// AgentVersion 定义了一个 Agent 的不可变版本
// 方便迭代，使新发布的版本不影响已有的版本，不是原地修改而是通过发布新版本的形式
/*
例如：
Agent：跨境电商运营助手

Version 1：
  SystemPrompt = "你是电商客服助手"

Version 2：
  SystemPrompt = "你是企业内部电商运营助手"
  Tools = [...]

  发布新版时，不修改version1，而是创建version2
  方便：回滚、审计、复现历史行为、维持旧会话行为
*/
type AgentVersion struct {
	ID           string
	AgentID      string
	WorkspaceID  string
	Version      int
	SystemPrompt string
	CreatedAt    time.Time
}

// Conversation 定义会话，要与 AgentVersion绑定
// 表示一次持续的聊天会话
// AgentVersion字段表示这个会话固定使用哪个Agent版本，之后发布了新版本也继续使用旧版本
type Conversation struct {
	ID             string
	AgentID        string
	AgentVersionID string
	UserID         string
	CreatedAt      time.Time
}
