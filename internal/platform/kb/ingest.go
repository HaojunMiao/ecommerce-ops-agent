package kb

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/util"
)

// ingestTracer 给 ingest 各 stage 打 kb.ingest.{stage} span。
var ingestTracer = otel.Tracer("kbot/kb/ingest")

// Ingest 阶段。
const (
	StageParse = "parse"
	StageChunk = "chunk"
	StageEmbed = "embed"
	StageIndex = "index"
	StageDone  = "done"
)

// IngestDocument 兼容同步调用：读取文档当前版本后执行版本化 ingest。
func (s *Service) IngestDocument(ctx context.Context, kbID, documentID, content string) error {
	doc, err := s.store.GetDocument(ctx, documentID)
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}
	if doc == nil {
		return fmt.Errorf("document %s not found", documentID)
	}
	return s.IngestDocumentVersion(ctx, kbID, documentID, doc.Hash, doc.EmbeddingIdentity, content)
}

// IngestDocumentVersion 跑完一份确定版本文档的 ingest 管道：parse → chunk → embed → index → done。
//
// 各 stage 在 worker 内顺序执行，并把状态写入 KbIngestJob。
// PostgreSQL 路径会在同一事务中锁定文档、校验 fingerprint/embedding identity、
// 替换 chunks 并标记 processed，形成 compare-and-swap，旧任务只能正常退出。
func (s *Service) IngestDocumentVersion(
	ctx context.Context,
	kbID, documentID, fingerprint, embeddingIdentity, content string,
) error {
	current, err := s.currentDocumentVersion(ctx, kbID, documentID, fingerprint, embeddingIdentity)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	job := &domain.KbIngestJob{
		ID:        util.GenerateID(),
		KbID:      kbID,
		DocID:     documentID,
		Stage:     StageParse,
		StartedAt: time.Now(),
	}
	if err := s.store.CreateIngestJob(ctx, job); err != nil {
		return fmt.Errorf("create ingest job: %w", err)
	}

	ctx, span := ingestTracer.Start(ctx, "kb.ingest",
		trace.WithAttributes(attribute.String("kb.id", kbID), attribute.String("doc.id", documentID)))
	defer span.End()

	fail := func(stage string, err error) error {
		msg := err.Error()
		job.Stage = stage
		job.Error = &msg
		_ = s.store.UpdateIngestJob(ctx, job)
		_, _ = s.store.MarkDocumentStatusIfCurrent(ctx, documentID, fingerprint, embeddingIdentity, "error")
		span.RecordError(err)
		return fmt.Errorf("ingest stage %s: %w", stage, err)
	}

	// setStage:更新 job 状态机 + 开一个 stage 子 span(调用方负责 End)。
	setStage := func(stage string) trace.Span {
		job.Stage = stage
		_ = s.store.UpdateIngestJob(ctx, job)
		_, sp := ingestTracer.Start(ctx, "kb.ingest."+stage)
		return sp
	}

	// 1) parse：当前接收 Markdown 和纯文本。
	sp := setStage(StageParse)
	parsed := content
	sp.End()

	// 2) chunk
	sp = setStage(StageChunk)
	texts := chunkText(parsed, s.chunkSize, s.overlap)
	sp.SetAttributes(attribute.Int("chunks", len(texts)))
	sp.End()
	if len(texts) == 0 {
		return fail(StageChunk, fmt.Errorf("文档切片为空"))
	}

	// 3) embed
	sp = setStage(StageEmbed)
	embs, err := s.retriever.Embedder().Embed(ctx, texts)
	sp.End()
	if err != nil {
		return fail(StageEmbed, err)
	}

	// 4) index
	sp = setStage(StageIndex)
	chunks := make([]retriever.Chunk, len(texts))
	for i, t := range texts {
		chunks[i] = retriever.Chunk{
			ID:        fmt.Sprintf("%s#%d", documentID, i),
			DocID:     documentID,
			Ordinal:   i,
			Content:   t,
			Embedding: embs[i],
		}
	}
	indexed := true
	if versioned, ok := s.retriever.(retriever.VersionedIndexer); ok {
		indexed, err = versioned.IndexIfCurrent(ctx, kbID, documentID, fingerprint, embeddingIdentity, chunks)
	} else {
		// 内存实现用于单测/无数据库模式。索引前再次校验，随后用条件状态更新
		// 防止旧任务把新文档标记为 processed；生产 PostgreSQL 使用上面的原子路径。
		indexed, err = s.currentDocumentVersion(ctx, kbID, documentID, fingerprint, embeddingIdentity)
		if err == nil && indexed {
			err = s.retriever.Index(ctx, kbID, chunks)
		}
	}
	sp.End()
	if err != nil {
		return fail(StageIndex, err)
	}
	if !indexed {
		job.Stage = StageDone
		now := time.Now()
		job.FinishedAt = &now
		_ = s.store.UpdateIngestJob(ctx, job)
		return nil
	}
	if _, ok := s.retriever.(retriever.VersionedIndexer); !ok {
		marked, err := s.store.MarkDocumentStatusIfCurrent(ctx, documentID, fingerprint, embeddingIdentity, "processed")
		if err != nil {
			return fail(StageIndex, err)
		}
		if !marked {
			job.Stage = StageDone
			now := time.Now()
			job.FinishedAt = &now
			_ = s.store.UpdateIngestJob(ctx, job)
			return nil
		}
	}

	// 5) done
	job.Stage = StageDone
	now := time.Now()
	job.FinishedAt = &now
	_ = s.store.UpdateIngestJob(ctx, job)
	_, err = s.store.ActivateKBIfReady(ctx, kbID, embeddingIdentity)
	if err != nil {
		return fmt.Errorf("activate kb after ingest: %w", err)
	}
	return nil
}

func (s *Service) currentDocumentVersion(
	ctx context.Context, kbID, documentID, fingerprint, embeddingIdentity string,
) (bool, error) {
	// 配置切换并重启后，Redis 中可能仍有旧模型任务。即使数据库行尚未进入
	// 重建流程，也不能用当前模型生成向量后再把它标成旧模型空间。
	if embeddingIdentity != s.retriever.Embedder().Identity() {
		return false, nil
	}
	doc, err := s.store.GetDocument(ctx, documentID)
	if err != nil {
		return false, fmt.Errorf("get document: %w", err)
	}
	if doc == nil {
		return false, nil
	}
	return doc.KbID == kbID && doc.Hash == fingerprint &&
		doc.EmbeddingIdentity == embeddingIdentity && (doc.Status == "pending" || doc.Status == "error"), nil
}

// ListIngestJobs 返回某 KB 的 ingest 任务记录。
func (s *Service) ListIngestJobs(ctx context.Context, kbID string) ([]*domain.KbIngestJob, error) {
	return s.store.ListIngestJobs(ctx, kbID)
}
