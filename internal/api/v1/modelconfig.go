package v1

import (
	"net/http"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/middleware"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/modelconfig"
)

// ModelConfigHandler exposes immutable model configuration versions without secrets.
type ModelConfigHandler struct{ svc *modelconfig.Service }

func NewModelConfigHandler(svc *modelconfig.Service) *ModelConfigHandler {
	return &ModelConfigHandler{svc: svc}
}

// ListConfigVersions lists all immutable versions in the current workspace.
// @Summary  模型配置版本列表
// @Tags     model-configs
// @Security BearerAuth
// @Success  200 {array} modelconfig.ModelConfigVersion
// @Router   /model-config-versions [get]
func (h *ModelConfigHandler) ListConfigVersions(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListConfigVersions(r.Context(), middleware.GetWorkspaceIDFromContext(r.Context()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
