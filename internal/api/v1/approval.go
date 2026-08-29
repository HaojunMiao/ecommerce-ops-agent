package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hibiken/asynq"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/middleware"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/jobs"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/audit"
)

// ApprovalHandler 处理人在环审批队列。
type ApprovalHandler struct {
	store approval.Store
	jobs  *jobs.Client
	audit *audit.Service
}

func NewApprovalHandler(store approval.Store, jobsClient *jobs.Client, auditServices ...*audit.Service) *ApprovalHandler {
	h := &ApprovalHandler{store: store, jobs: jobsClient}
	if len(auditServices) > 0 {
		h.audit = auditServices[0]
	}
	return h
}

// List 返回待审批队列。
// @Summary  待审批队列
// @Tags     approvals
// @Security BearerAuth
// @Success  200  {array}  map[string]interface{}
// @Router   /approvals [get]
func (h *ApprovalHandler) List(w http.ResponseWriter, r *http.Request) {
	pend, err := h.store.ListPending(r.Context(), middleware.GetWorkspaceIDFromContext(r.Context()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, pend)
}

// Approve 批准一条审批:标 approved → enqueue engine_resume 让 worker 拉 checkpoint 续跑。
// @Summary  批准审批(触发续跑)
// @Tags     approvals
// @Security BearerAuth
// @Param    approval_id  path      string  true  "审批 ID"
// @Success  200          {object}  map[string]interface{}
// @Router   /approvals/{approval_id}/approve [post]
func (h *ApprovalHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "approval_id")
	approverID := middleware.GetUserIDFromContext(r.Context())
	appr, err := h.resolve(r.Context(), id, approval.StatusApproved, approverID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": approval.StatusApproved, "conversation_id": appr.ConversationID})
}

// Reject 拒绝一条审批(不续跑)。
// @Summary  拒绝审批并释放会话
// @Tags     approvals
// @Security BearerAuth
// @Param    approval_id path string true "审批 ID"
// @Success  200 {object} map[string]interface{}
// @Router   /approvals/{approval_id}/reject [post]
func (h *ApprovalHandler) Reject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "approval_id")
	approverID := middleware.GetUserIDFromContext(r.Context())
	if _, err := h.resolve(r.Context(), id, approval.StatusRejected, approverID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": approval.StatusRejected})
}

func (h *ApprovalHandler) resolve(ctx context.Context, id, status, approverID string) (*approval.Approval, error) {
	appr, err := h.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return h.resolveKnown(ctx, appr, status, approverID)
}

func (h *ApprovalHandler) resolveKnown(ctx context.Context, appr *approval.Approval, status, approverID string) (*approval.Approval, error) {
	workspaceID := middleware.GetWorkspaceIDFromContext(ctx)
	if appr.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("approval not found")
	}
	resolved, err := h.store.ResolvePending(ctx, appr.ID, workspaceID, status, approverID)
	if err != nil {
		return nil, err
	}
	if status == approval.StatusApproved && h.jobs != nil && resolved.ConversationID != "" {
		payload, _ := json.Marshal(jobs.ResumePayload{ConversationID: resolved.ConversationID, ApprovalID: resolved.ID})
		if _, err := h.jobs.Enqueue(
			asynq.NewTask(jobs.TypeEngineResume, payload),
			asynq.TaskID("approval-resume-"+strings.ReplaceAll(resolved.ID, "-", "")),
		); err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil, fmt.Errorf("enqueue resume: %w", err)
		}
	}
	if h.audit != nil {
		h.audit.RecordWorkspace(workspaceID, approverID, "approval_"+status, "approval", resolved.ID)
	}
	return resolved, nil
}
