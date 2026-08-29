package v1

import (
	"net/http"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/middleware"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/audit"
)

// AuditHandler 审计检索处理器
type AuditHandler struct {
	svc *audit.Service
}

// NewAuditHandler 创建审计处理器
func NewAuditHandler(svc *audit.Service) *AuditHandler {
	return &AuditHandler{svc: svc}
}

// Logs 按 conversation_id / actor 检索审计日志
// @Summary  检索审计日志
// @Tags     audit
// @Security BearerAuth
// @Param    conversation_id  query     string  false  "会话 ID"
// @Param    actor            query     string  false  "操作者"
// @Param    limit            query     int     false  "条数"
// @Success  200              {array}   map[string]interface{}
// @Router   /audit/logs [get]
func (h *AuditHandler) Logs(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 100, 1, 1000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logs, err := h.svc.Query(r.Context(), audit.QueryFilter{
		WorkspaceID:    middleware.GetWorkspaceIDFromContext(r.Context()),
		ConversationID: r.URL.Query().Get("conversation_id"),
		Actor:          r.URL.Query().Get("actor"),
		Limit:          limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}
