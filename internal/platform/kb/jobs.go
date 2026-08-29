package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// TypeIngestDocument 是 KB 文档 ingest 的 asynq 任务类型。
const TypeIngestDocument = "kb_ingest_document"

// IngestPayload 是异步 ingest 任务的载荷。
type IngestPayload struct {
	KbID              string `json:"kb_id"`
	DocumentID        string `json:"document_id"`
	Content           string `json:"content"`
	Fingerprint       string `json:"fingerprint"`
	EmbeddingIdentity string `json:"embedding_identity"`
}

// NewIngestTask 构造一个 ingest 任务。
//
// 同一载荷只在短窗口内去重。任务成功或窗口到期后可以重新投递，因此修复网络、
// API Key 等问题后再次同步不会被 Asynq Archive 中的固定 TaskID 长期阻塞。
// PostgreSQL 的版本校验事务负责最终正确性，队列去重只负责减少重复计算。
func NewIngestTask(p IngestPayload) (*asynq.Task, error) {
	if p.KbID == "" || p.DocumentID == "" || p.Fingerprint == "" || p.EmbeddingIdentity == "" {
		return nil, fmt.Errorf("ingest payload requires kb_id, document_id, fingerprint and embedding_identity")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeIngestDocument, b, asynq.Unique(10*time.Minute), asynq.Queue("default")), nil
}

// HandleIngest 返回处理 ingest 任务的 asynq.HandlerFunc。失败返回 error，
// 由 asynq 按退避重试（§4.10）。
func (s *Service) HandleIngest() asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p IngestPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal ingest payload: %w", err)
		}
		// 部署前已经排队的旧载荷没有版本信息，无法证明仍是当前文档；安全丢弃，
		// 下一次 Connector 同步会按当前指纹重新投递。
		if p.Fingerprint == "" || p.EmbeddingIdentity == "" {
			return nil
		}
		return s.IngestDocumentVersion(ctx, p.KbID, p.DocumentID, p.Fingerprint, p.EmbeddingIdentity, p.Content)
	}
}
