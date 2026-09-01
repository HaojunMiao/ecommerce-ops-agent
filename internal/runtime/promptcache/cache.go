// Package promptcache 缓存不可变 PromptVersion 的编译产物。
// 版本内容不可变，因此缓存直接按 versionID 命中，无需环境指针或失效广播。
package promptcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	einoprompt "github.com/cloudwego/eino/components/prompt"
)

// Compiled 是一个 Prompt 版本的编译产物。
type Compiled struct {
	VersionID    string
	Raw          string
	Tmpl         einoprompt.ChatTemplate
	RequiredVars []string // 变量 Schema 里 required 的键(保存时解析)
	EstTokens    int
	UpdatedAt    time.Time
}

// Render 用给定变量渲染模板。missingkey=error 保证缺变量立刻报错而非渲染成 <no value>。
func (c *Compiled) Render(ctx context.Context, vars map[string]any) (string, error) {
	// 先做 required 校验，给出比 template 更清晰的错误。
	for _, k := range c.RequiredVars {
		if _, ok := vars[k]; !ok {
			return "", fmt.Errorf("prompt 缺少必填变量 %q", k)
		}
	}
	if c.Tmpl == nil {
		return c.Raw, nil
	}
	messages, err := c.Tmpl.Format(ctx, vars)
	if err != nil {
		return "", fmt.Errorf("render prompt: %w", err)
	}
	if len(messages) != 1 {
		return "", fmt.Errorf("render prompt: expected one message, got %d", len(messages))
	}
	return messages[0].Content, nil
}

// Cache 是 versionID → *Compiled 的本地缓存。
type Cache struct {
	mp sync.Map
}

// NewCache 创建缓存。
func NewCache() *Cache { return &Cache{} }

// Get 取某个不可变 PromptVersion 的编译产物。
func (c *Cache) Get(versionID string) (*Compiled, bool) {
	v, ok := c.mp.Load(versionID)
	if !ok {
		return nil, false
	}
	return v.(*Compiled), true
}

// Put 写入编译产物。
func (c *Cache) Put(versionID string, comp *Compiled) {
	c.mp.Store(versionID, comp)
}

// Invalidate 仅供测试或显式清理；正常版本不会被更新，无需失效。
func (c *Cache) Invalidate(versionID string) {
	c.mp.Delete(versionID)
}
