package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
)

// ApprovalLeaseStore 是后台恢复执行所需的最小审批存储接口。
// Claim/Complete/Fail 都带隔离令牌，避免租约过期后两个 Worker 同时提交结果。
type ApprovalLeaseStore interface {
	ListReady(ctx context.Context, limit int) ([]approval.Request, error)
	ClaimExecution(ctx context.Context, workspaceID, requestID, runID, toolCallID, toolVersionID, workerID string, arguments []byte, leaseDuration time.Duration) (*approval.Lease, error)
	Complete(ctx context.Context, workspaceID, requestID string, token uint64) error
	Fail(ctx context.Context, workspaceID, requestID string, token uint64, executionErr error, maxAttempts int) error
}

type ApprovedResumer interface {
	ResumeApproved(ctx context.Context, request *approval.Request, checkpoint []byte, emit Emitter) error
}

// ApprovalWorker 扫描已批准或租约过期的任务，并从持久化检查点恢复原运行。
// 第 17 课仍把它放在 API 服务进程内；后续最终版才拆成独立异步 Worker。
type ApprovalWorker struct {
	store        ApprovalLeaseStore
	resumer      ApprovedResumer
	workerID     string
	lease        time.Duration
	pollInterval time.Duration
	maxAttempts  int
	wake         chan struct{}
}

func NewApprovalWorker(store ApprovalLeaseStore, resumer ApprovedResumer, workerID string) *ApprovalWorker {
	return &ApprovalWorker{
		store: store, resumer: resumer, workerID: workerID,
		lease: time.Minute, pollInterval: time.Second, maxAttempts: 3, wake: make(chan struct{}, 1),
	}
}

// Wake 采用容量为 1 的通知通道：多次批准只需留下一个“立即再扫一次”的信号，
// 不会让 HTTP 请求因为 Worker 忙碌而阻塞。
func (w *ApprovalWorker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *ApprovalWorker) Run(ctx context.Context) error {
	if w.store == nil || w.resumer == nil || w.workerID == "" {
		return fmt.Errorf("approval worker dependencies are required")
	}
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.runBatch(ctx); err != nil && ctx.Err() == nil {
			// 保留轮询；单个坏任务会进入有界重试并最终 failed。
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-w.wake:
		}
	}
}

func (w *ApprovalWorker) runBatch(ctx context.Context) error {
	requests, err := w.store.ListReady(ctx, 20)
	if err != nil {
		return err
	}
	for index := range requests {
		request := &requests[index]
		lease, err := w.store.ClaimExecution(
			ctx, request.WorkspaceID, request.ID, request.RunID, request.ToolCallID,
			request.ToolVersionID, w.workerID, request.Arguments, w.lease,
		)
		if err != nil {
			// 可能已被另一个 Worker 抢到，跳过即可。
			continue
		}
		err = w.resumer.ResumeApproved(ctx, &lease.Request, lease.Checkpoint, func(Event) error { return nil })
		if err != nil {
			_ = w.store.Fail(ctx, request.WorkspaceID, request.ID, lease.Token, err, w.maxAttempts)
			continue
		}
		if err := w.store.Complete(ctx, request.WorkspaceID, request.ID, lease.Token); err != nil {
			return err
		}
	}
	return nil
}
