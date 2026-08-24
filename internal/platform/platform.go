package platform

import (
	"context"
	"fmt"
	"sync"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/engine"
)

// Agent控制面的一些配置
// 负责“Agent应该怎么运行”的配置
// prompt、tool、skill等都会在这里管理

// Platform 控制面的入口
// 内存模拟数据库，存储 所有会话 和 agentSnapshot（Agent实现版本）
type Platform struct {
	mu            sync.RWMutex
	conversations map[string]*domain.Conversation
	snapshots     map[string]*engine.AgentSnapshot
}

// New 创建新的控制面
func New() *Platform {
	return &Platform{
		conversations: make(map[string]*domain.Conversation),
		snapshots:     make(map[string]*engine.AgentSnapshot),
	}
}

func (p *Platform) PutConversation(conversation *domain.Conversation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copy := *conversation
	p.conversations[copy.ID] = &copy
}

func (p *Platform) PutSnapshot(snapshot *engine.AgentSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copy := *snapshot
	p.snapshots[copy.ID] = &copy
}

func (p *Platform) LoadConversation(_ context.Context, id string) (*domain.Conversation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	conversation, ok := p.conversations[id]
	if !ok {
		return nil, fmt.Errorf("conversation %s not found", id)
	}
	copy := *conversation
	return &copy, nil
}

func (p *Platform) GetAgentSnapshotByVersion(_ context.Context, id string) (*engine.AgentSnapshot, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snapshot, ok := p.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("agent snapshot %s not found", id)
	}
	copy := *snapshot
	return &copy, nil
}

var _ engine.Platform = (*Platform)(nil)
