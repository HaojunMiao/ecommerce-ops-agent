package kb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/util"
)

// memStore 是测试用的最小内存 KB 存储。
type memStore struct {
	mu         sync.Mutex
	kbs        map[string]*domain.KnowledgeBase
	docs       map[string]*domain.KbDocument
	jobs       map[string][]*domain.KbIngestJob
	connectors map[string][]*domain.ConnectorInstance
}

func newMemStore() *memStore {
	return &memStore{
		kbs:        map[string]*domain.KnowledgeBase{},
		docs:       map[string]*domain.KbDocument{},
		jobs:       map[string][]*domain.KbIngestJob{},
		connectors: map[string][]*domain.ConnectorInstance{},
	}
}

func (s *memStore) CreateKB(_ context.Context, kb *domain.KnowledgeBase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kbs[kb.ID] = kb
	return nil
}
func (s *memStore) GetKB(_ context.Context, id string) (*domain.KnowledgeBase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, os.ErrNotExist
}
func (s *memStore) ListKBs(_ context.Context, ws string) ([]*domain.KnowledgeBase, error) {
	return nil, nil
}
func (s *memStore) UpdateKBStatus(_ context.Context, id, status string) error { return nil }
func (s *memStore) UpdateKBEmbeddingModel(_ context.Context, id, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kbs[id].EmbeddingModel = model
	return nil
}
func (s *memStore) UpdateKBChunkingConfig(_ context.Context, id, chunkingConfig string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kbs[id].ChunkingConfig = chunkingConfig
	return nil
}
func (s *memStore) BeginKBReindex(_ context.Context, id, identity string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kbs[id].EmbeddingModel = identity
	s.kbs[id].Status = "indexing"
	for _, doc := range s.docs {
		if doc.KbID == id {
			doc.Status = "pending"
			doc.IngestedAt = nil
			doc.EmbeddingIdentity = identity
		}
	}
	return nil
}
func (s *memStore) ActivateKBIfReady(_ context.Context, id, identity string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range s.docs {
		if doc.KbID == id && (doc.Status != "processed" || doc.EmbeddingIdentity != identity) {
			return false, nil
		}
	}
	s.kbs[id].Status = "active"
	return true, nil
}
func (s *memStore) UpsertDocument(_ context.Context, d *domain.KbDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[d.ID] = d
	return nil
}
func (s *memStore) GetDocument(_ context.Context, id string) (*domain.KbDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.docs[id]; ok {
		return d, nil
	}
	return nil, nil
}
func (s *memStore) ListDocuments(_ context.Context, kbID string) ([]*domain.KbDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.KbDocument
	for _, doc := range s.docs {
		if doc.KbID == kbID {
			out = append(out, doc)
		}
	}
	return out, nil
}
func (s *memStore) DeleteDocument(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, id)
	return nil
}
func (s *memStore) MarkDocumentStatusIfCurrent(_ context.Context, id, fingerprint, identity, status string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[id]
	if doc == nil || doc.Hash != fingerprint || doc.EmbeddingIdentity != identity ||
		(doc.Status != "pending" && doc.Status != "error") {
		return false, nil
	}
	doc.Status = status
	if status == "processed" {
		now := time.Now()
		doc.IngestedAt = &now
	} else {
		doc.IngestedAt = nil
	}
	return true, nil
}
func (s *memStore) CreateIngestJob(_ context.Context, j *domain.KbIngestJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.KbID] = append(s.jobs[j.KbID], j)
	return nil
}
func (s *memStore) UpdateIngestJob(_ context.Context, j *domain.KbIngestJob) error { return nil }
func (s *memStore) ListIngestJobs(_ context.Context, kbID string) ([]*domain.KbIngestJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[kbID], nil
}
func (s *memStore) UpsertConnector(_ context.Context, c *domain.ConnectorInstance) error {
	s.connectors[c.KbID] = append(s.connectors[c.KbID], c)
	return nil
}
func (s *memStore) ListConnectors(_ context.Context, kbID string) ([]*domain.ConnectorInstance, error) {
	return s.connectors[kbID], nil
}

func newTestService() (*Service, *memStore) {
	store := newMemStore()
	r := retriever.New(retriever.NewLocalEmbedder(256))
	return NewService(store, r, nil), store
}

func TestIngestAndSearch(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	kb, err := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	if err != nil {
		t.Fatalf("create kb: %v", err)
	}

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "refund.md"),
		"# 退款政策\n\n用户在七天内可以申请退款。需要提供订单号。\n\n超过七天不支持退款。")
	mustWrite(t, filepath.Join(dir, "shipping.md"),
		"# 物流\n\n标准快递三到五个工作日送达。")

	res, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Ingested != 2 {
		t.Fatalf("expected 2 ingested, got %+v", res)
	}

	// 检索应命中退款文档。
	passages, err := svc.Search(ctx, kb.ID, "怎么申请退款", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(passages) == 0 {
		t.Fatal("expected search results")
	}
	// 退款相关片段应排在最前。
	if !strings.Contains(passages[0].Text, "退款") {
		t.Fatalf("expected top passage about 退款, got %q", passages[0].Text)
	}

	// ingest 任务应记录 done 阶段。
	jobs, _ := svc.ListIngestJobs(ctx, kb.ID)
	if len(jobs) != 2 {
		t.Fatalf("expected 2 ingest jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.Stage != StageDone {
			t.Fatalf("expected job stage done, got %s (err=%v)", j.Stage, j.Error)
		}
	}
}

func TestSyncMarkdownFolderAllowedRejectsOutsideRootAndSymlinkEscape(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	kb, err := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "Allowed", CreatedBy: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	allowed := t.TempDir()
	inside := filepath.Join(allowed, "course")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(inside, "inside.md"), "inside")
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.md"), "secret")
	svc.ConfigureMarkdownAllowedRoots([]string{allowed})

	if _, err := svc.SyncMarkdownFolderAllowed(ctx, kb.ID, inside); err != nil {
		t.Fatalf("allowed root rejected: %v", err)
	}
	if _, err := svc.SyncMarkdownFolderAllowed(ctx, kb.ID, outside); err == nil {
		t.Fatal("outside root should be rejected")
	}
	link := filepath.Join(allowed, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncMarkdownFolderAllowed(ctx, kb.ID, link); err == nil {
		t.Fatal("symlink escaping allowed root should be rejected")
	}
}

func TestSyncSkipsUnchanged(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	kb, _ := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.md"), "内容")

	first, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir)
	if err != nil || first.Ingested != 1 {
		t.Fatalf("first sync: %+v err=%v", first, err)
	}

	// 再同步一次：hash 未变，应跳过。
	second, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second.Ingested != 0 || second.Skipped != 1 {
		t.Fatalf("expected skip on unchanged content, got %+v", second)
	}
}

func TestSyncDeletesDocumentMissingFromSnapshot(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	kb, _ := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	dir := t.TempDir()
	path := filepath.Join(dir, "obsolete.md")
	mustWrite(t, path, "已经废弃的退款规则")
	if _, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir)
	if err != nil {
		t.Fatalf("sync after delete: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("expected one deleted document, got %+v", result)
	}
	docs, _ := store.ListDocuments(ctx, kb.ID)
	if len(docs) != 0 {
		t.Fatalf("deleted source still present in store: %+v", docs)
	}
	passages, err := svc.Search(ctx, kb.ID, "退款规则", 5)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(passages) != 0 {
		t.Fatalf("deleted source still searchable: %+v", passages)
	}
}

func TestStaleIngestPayloadIsIgnored(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	kb, _ := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	doc := &domain.KbDocument{
		ID: util.GenerateID(), KbID: kb.ID, SourceType: "connector", SourceURI: "/a.md",
		Hash: "new-fingerprint", EmbeddingIdentity: svc.retriever.Embedder().Identity(), Status: "pending",
	}
	if err := store.UpsertDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestDocumentVersion(ctx, kb.ID, doc.ID, "old-fingerprint", doc.EmbeddingIdentity, "旧内容"); err != nil {
		t.Fatalf("stale ingest should be a no-op: %v", err)
	}
	got, _ := store.GetDocument(ctx, doc.ID)
	if got.Status != "pending" || got.Hash != "new-fingerprint" {
		t.Fatalf("stale task changed current document: %+v", got)
	}
}

func TestTaskFromRetiredEmbedderIsIgnored(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	kb, _ := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	doc := &domain.KbDocument{
		ID: util.GenerateID(), KbID: kb.ID, SourceType: "connector", SourceURI: "/a.md",
		Hash: "old-fingerprint", EmbeddingIdentity: "retired-embedder", Status: "pending",
	}
	if err := store.UpsertDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestDocumentVersion(ctx, kb.ID, doc.ID, doc.Hash, doc.EmbeddingIdentity, "旧模型内容"); err != nil {
		t.Fatalf("retired embedder task should be a no-op: %v", err)
	}
	got, _ := store.GetDocument(ctx, doc.ID)
	if got.Status != "pending" {
		t.Fatalf("retired embedder task changed document: %+v", got)
	}
}

func TestLateFailureCannotDowngradeProcessedDocument(t *testing.T) {
	_, store := newTestService()
	doc := &domain.KbDocument{
		ID: util.GenerateID(), KbID: util.GenerateID(), Hash: "fingerprint",
		EmbeddingIdentity: "embedder", Status: "processed",
	}
	if err := store.UpsertDocument(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	updated, err := store.MarkDocumentStatusIfCurrent(
		context.Background(), doc.ID, doc.Hash, doc.EmbeddingIdentity, "error",
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated || doc.Status != "processed" {
		t.Fatalf("late failure downgraded processed document: updated=%v doc=%+v", updated, doc)
	}
}

func TestIngestTaskPayloadCarriesFingerprint(t *testing.T) {
	base := IngestPayload{KbID: "kb", DocumentID: "doc", Content: "x", EmbeddingIdentity: "embedder"}
	base.Fingerprint = "v1"
	task, err := NewIngestTask(base)
	if err != nil {
		t.Fatal(err)
	}
	var got IngestPayload
	if err := json.Unmarshal(task.Payload(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != base.Fingerprint || got.EmbeddingIdentity != base.EmbeddingIdentity {
		t.Fatalf("version fields missing from task payload: %+v", got)
	}
}

func TestSyncReindexesWhenEmbedderIdentityChanges(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	kb, _ := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.md"), "库存不足时执行跨仓调拨")

	if first, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir); err != nil || first.Ingested != 1 {
		t.Fatalf("first sync: %+v err=%v", first, err)
	}
	changed := NewService(store, retriever.New(retriever.NewLocalEmbedder(512)), nil)
	second, err := changed.SyncMarkdownFolder(ctx, kb.ID, dir)
	if err != nil {
		t.Fatalf("sync after embedder change: %v", err)
	}
	if second.Ingested != 1 || second.Skipped != 0 {
		t.Fatalf("embedder change must trigger reindex, got %+v", second)
	}
}

func TestSyncReindexesWhenChunkingConfigChanges(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	kb, _ := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.md"), strings.Repeat("跨境退款规则。", 200))

	if first, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir); err != nil || first.Ingested != 1 {
		t.Fatalf("first sync: %+v err=%v", first, err)
	}
	store.kbs[kb.ID].ChunkingConfig = `{"size":500,"overlap":100}`
	second, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir)
	if err != nil {
		t.Fatalf("sync after chunking change: %v", err)
	}
	if second.Ingested != 1 || second.Skipped != 0 {
		t.Fatalf("chunking change must trigger reindex, got %+v", second)
	}
	if got := store.kbs[kb.ID].ChunkingConfig; got != `{"size":1200,"overlap":200}` {
		t.Fatalf("chunking config = %s", got)
	}
}

func TestSyncRejectsOversizedDocument(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	kb, err := svc.CreateKB(ctx, CreateKBRequest{WorkspaceID: "w1", Name: "FAQ", CreatedBy: "u1"})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "large.md"), strings.Repeat("x", maxDocumentBytes+1))
	if _, err := svc.SyncMarkdownFolder(ctx, kb.ID, dir); err == nil {
		t.Fatal("oversized document was accepted")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
