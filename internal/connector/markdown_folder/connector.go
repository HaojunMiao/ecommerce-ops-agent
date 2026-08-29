// Package markdown_folder 是 reference Connector：监听本地 Markdown 文件夹。
//
// 简单可控。
package markdown_folder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/connector"
)

// Connector 监听一个本地文件夹下的所有 .md 文件。
type Connector struct {
	rootPath string
}

// New 创建一个 markdown_folder connector。
func New(rootPath string) *Connector {
	return &Connector{rootPath: rootPath}
}

func (c *Connector) Name() string { return "markdown_folder" }

// OwnsSource 判断历史文档 URI 是否属于当前目录。源文件可能已经删除，因此这里只做
// 规范化路径包含关系，不对 sourceURI 调用 EvalSymlinks。
func (c *Connector) OwnsSource(sourceURI string) bool {
	root, err := filepath.Abs(c.rootPath)
	if err != nil {
		return false
	}
	if real, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = real
	}
	source, err := filepath.Abs(sourceURI)
	if err != nil {
		return false
	}
	// 文件本身可能已经不存在，但父目录仍可解析（macOS /var → /private/var
	// 这类系统级符号链接也需要与 root 使用同一种规范化方式）。
	if realParent, evalErr := filepath.EvalSymlinks(filepath.Dir(source)); evalErr == nil {
		source = filepath.Join(realParent, filepath.Base(source))
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(source))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ListDocuments 遍历根目录下所有 .md 文件。文件夹通常不大，一次 list 全量返回，
// 不用游标分页（cursor 始终返回空）。
func (c *Connector) ListDocuments(ctx context.Context, cursor string) ([]connector.DocMeta, string, error) {
	var docs []connector.DocMeta
	err := filepath.WalkDir(c.rootPath, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := fileHash(path)
		if err != nil {
			return err
		}
		docs = append(docs, connector.DocMeta{
			ID:        path,
			Title:     filepath.Base(path),
			UpdatedAt: info.ModTime(),
			Hash:      h,
		})
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("walk markdown folder: %w", err)
	}
	return docs, "", nil
}

// FetchDocument 打开一份文档供读取。docID 即文件路径。
func (c *Connector) FetchDocument(ctx context.Context, docID string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(docID)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(c.rootPath)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("document %q outside connector root", docID)
	}
	return os.Open(abs)
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
