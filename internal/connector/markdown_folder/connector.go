// Package markdown_folder 从本地 Markdown 目录读取课堂知识库。
package markdown_folder

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/connector"
)

// Connector 从 Root 开始递归扫描本地 Markdown 文件。
type Connector struct{ Root string }

func New(root string) *Connector { return &Connector{Root: root} }

// Scan 将目录中的每个 .md 文件转换成一个标准 Document。
// 它只负责读取和标准化文档，不负责切片与建立检索索引。
func (c *Connector) Scan(ctx context.Context) ([]connector.Document, error) {
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	var documents []connector.Document
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// 目录较大时允许上游通过 context 及时取消扫描。
		if err := ctx.Err(); err != nil {
			return err
		}
		// 不跟随符号链接，避免扫描范围逃逸到 Root 之外。
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		documents = append(documents, connector.Document{
			// 保存相对路径而不是机器上的绝对路径，使来源标识更稳定、可迁移。
			SourceURI: filepath.ToSlash(relative),
			Title:     titleOf(entry.Name(), string(content)),
			Content:   string(content),
			Checksum:  fmt.Sprintf("%x", sum),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan markdown folder: %w", err)
	}

	// 文件系统遍历顺序不应成为接口行为的一部分，显式排序保证结果稳定。
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].SourceURI < documents[j].SourceURI
	})
	return documents, nil
}

// titleOf 只把第一个一级标题作为文档标题；它不会按标题拆分文档。
// 没有一级标题时退化为不带扩展名的文件名。
func titleOf(filename, content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		if title := strings.TrimSpace(strings.TrimPrefix(line, "# ")); title != "" {
			return title
		}
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// 编译期确认本实现满足统一知识源接口。
var _ connector.Connector = (*Connector)(nil)
