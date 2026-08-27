// Package kb 管理课堂版知识库与显式 ingest 状态机。
package kb

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/connector"
)

// KnowledgeBase 是一个工作空间下的知识库容器。
type KnowledgeBase struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// Document 是知识库内部保存的文档。与 connector.Document 相比，
// 它增加了平台生成的 ID、所属知识库以及切片结果。
type Document struct {
	ID        string   `json:"id"`
	KBID      string   `json:"kb_id"`
	SourceURI string   `json:"source_uri"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Checksum  string   `json:"checksum"`
	Chunks    []string `json:"chunks"`
}

// IngestJob 描述一次知识导入的进度和统计结果。
// 第 10 课仍在请求内同步执行，它暂时只是状态记录，不是异步队列任务。
type IngestJob struct {
	ID         string     `json:"id"`
	KBID       string     `json:"kb_id"`
	Stage      string     `json:"stage"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Stages     []string   `json:"stages"`
	Listed     int        `json:"listed"`
	Ingested   int        `json:"ingested"`
	Skipped    int        `json:"skipped"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Service 是课堂版内存知识库服务。
// bases 按知识库 ID 保存元数据；documents 再按 kbID 和 SourceURI 两级索引文档。
type Service struct {
	mu        sync.RWMutex
	bases     map[string]KnowledgeBase
	documents map[string]map[string]Document
	sequence  atomic.Uint64
}

func NewService() *Service {
	return &Service{
		bases:     make(map[string]KnowledgeBase),
		documents: make(map[string]map[string]Document),
	}
}

// Create 在指定工作空间内创建一个空知识库。
func (s *Service) Create(_ context.Context, workspaceID, name string) (*KnowledgeBase, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("workspace and knowledge base name are required")
	}
	base := KnowledgeBase{
		ID: s.nextID("kb"), WorkspaceID: workspaceID, Name: strings.TrimSpace(name),
		Status: "ready", CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.bases[base.ID] = base
	s.documents[base.ID] = make(map[string]Document)
	s.mu.Unlock()
	return &base, nil
}

// List 只返回当前工作空间的知识库，避免跨工作空间读取。
func (s *Service) List(_ context.Context, workspaceID string) []KnowledgeBase {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]KnowledgeBase, 0, len(s.bases))
	for _, base := range s.bases {
		if base.WorkspaceID == workspaceID {
			result = append(result, base)
		}
	}
	return result
}

// Sync 运行一次显式的知识导入流水线：
// parse(读取标准文档) -> chunk(切片) -> embed(占位) -> index(保存) -> done。
func (s *Service) Sync(ctx context.Context, workspaceID, kbID string, source connector.Connector) (*IngestJob, error) {
	job := &IngestJob{
		ID: s.nextID("ingest"), KBID: kbID, Stage: "parse", Status: "running",
		CreatedAt: time.Now().UTC(),
	}
	if source == nil {
		return s.fail(job, "parse", fmt.Errorf("connector is required"))
	}
	if err := s.ensureWorkspace(workspaceID, kbID); err != nil {
		return s.fail(job, "parse", err)
	}

	// parse：Connector 屏蔽具体来源，知识库服务只接收统一 Document。
	documents, err := source.Scan(ctx)
	if err != nil {
		return s.fail(job, "parse", err)
	}
	job.Stages = append(job.Stages, "parse")
	job.Listed = len(documents)
	if len(documents) == 0 {
		return s.fail(job, "chunk", fmt.Errorf("connector returned no markdown documents"))
	}

	// 先在局部变量中完成全部切片。只有所有文档都处理成功，才整体写入 Service，
	// 避免切到一半失败时留下部分更新的数据。
	indexed := make(map[string]Document, len(documents))
	job.Stage = "chunk"
	for _, document := range documents {
		chunks := chunkText(document.Content, 500, 50)
		if len(chunks) == 0 {
			return s.fail(job, "chunk", fmt.Errorf("document %s has no indexable content", document.SourceURI))
		}
		indexed[document.SourceURI] = Document{
			ID: s.nextID("doc"), KBID: kbID, SourceURI: document.SourceURI,
			Title: document.Title, Content: document.Content, Checksum: document.Checksum,
			Chunks: chunks,
		}
	}
	job.Stages = append(job.Stages, "chunk")

	// 第 10 课尚未接入嵌入模型，embed 只是流程占位；index 也只是写入
	// 下方的内存 map，还不是真正的全文或向量检索索引。
	job.Stage = "embed"
	job.Stages = append(job.Stages, "embed")
	job.Stage = "index"

	// SourceURI 标识同一来源文档；Checksum 相同则复用原 ID 并计为 skipped。
	s.mu.Lock()
	for sourceURI, document := range indexed {
		if previous, ok := s.documents[kbID][sourceURI]; ok && previous.Checksum == document.Checksum {
			document.ID = previous.ID
			job.Skipped++
		} else {
			job.Ingested++
		}
		s.documents[kbID][sourceURI] = document
	}
	s.mu.Unlock()
	job.Stages = append(job.Stages, "index")

	now := time.Now().UTC()
	job.Stage, job.Status, job.FinishedAt = "done", "succeeded", &now
	job.Stages = append(job.Stages, "done")
	return job, nil
}

// Documents 返回知识库中的全部文档，并复制 Chunks 切片，避免调用方
// 通过返回值修改 Service 内部保存的切片底层数组。
func (s *Service) Documents(_ context.Context, workspaceID, kbID string) ([]Document, error) {
	if err := s.ensureWorkspace(workspaceID, kbID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Document, 0, len(s.documents[kbID]))
	for _, document := range s.documents[kbID] {
		document.Chunks = append([]string(nil), document.Chunks...)
		result = append(result, document)
	}
	return result, nil
}

// ensureWorkspace 同时完成“知识库存在”和“属于当前工作空间”的校验，
// 对外统一表现为 not found，避免跨工作空间枚举资源。
func (s *Service) ensureWorkspace(workspaceID, kbID string) error {
	s.mu.RLock()
	base, ok := s.bases[kbID]
	s.mu.RUnlock()
	if !ok || base.WorkspaceID != workspaceID {
		return fmt.Errorf("knowledge base %s not found", kbID)
	}
	return nil
}

func (s *Service) fail(job *IngestJob, stage string, err error) (*IngestJob, error) {
	now := time.Now().UTC()
	job.Stage, job.Status, job.Error, job.FinishedAt = stage, "failed", err.Error(), &now
	return job, err
}

// nextID 仅用于课堂内存实现。进程重启后会重新计数，不适合作为生产 ID 方案。
func (s *Service) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, s.sequence.Add(1))
}

// chunkText 按 Unicode 字符切片，避免按字节截断中文；overlap 让相邻切片
// 保留部分上下文，降低关键信息刚好落在边界时的语义损失。
func chunkText(content string, size, overlap int) []string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) == 0 || size <= 0 || overlap < 0 || overlap >= size {
		return nil
	}

	var chunks []string
	for start := 0; start < len(runes); start += size - overlap {
		end := min(start+size, len(runes))
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}
