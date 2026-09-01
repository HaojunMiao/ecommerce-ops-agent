package v1

import (
	"net/http"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/middleware"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/prompt"
	"github.com/go-chi/chi/v5"
)

// PromptHandler Prompt 中心处理器
type PromptHandler struct {
	svc *prompt.Service
}

// NewPromptHandler 创建 Prompt 处理器
func NewPromptHandler(svc *prompt.Service) *PromptHandler {
	return &PromptHandler{svc: svc}
}

// CreatePrompt 创建 Prompt（含不可变 v1）。
// @Summary  创建 Prompt(含 v1)
// @Tags     prompts
// @Security BearerAuth
// @Param    body  body      prompt.CreatePromptRequest  true  "Prompt"
// @Success  201   {object}  map[string]interface{}
// @Router   /prompts [post]
func (h *PromptHandler) CreatePrompt(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserIDFromContext(r.Context())
	workspaceID := middleware.GetWorkspaceIDFromContext(r.Context())

	var req prompt.CreatePromptRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.CreatedBy = userID
	req.WorkspaceID = workspaceID

	p, v, err := h.svc.CreatePrompt(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"prompt": p, "version": v})
}

// CreateVersion 新增 immutable 版本
func (h *PromptHandler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	promptID := chi.URLParam(r, "prompt_id")
	if !h.ensurePromptWorkspace(w, r, promptID) {
		return
	}
	userID := middleware.GetUserIDFromContext(r.Context())

	var body struct {
		Template        string `json:"template"`
		VariablesSchema string `json:"variables_schema"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	v, err := h.svc.CreateVersion(r.Context(), promptID, body.Template, body.VariablesSchema, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// Diff 语义级 diff（from→to）
func (h *PromptHandler) Diff(w http.ResponseWriter, r *http.Request) {
	promptID := chi.URLParam(r, "prompt_id")
	if !h.ensurePromptWorkspace(w, r, promptID) {
		return
	}
	from, err := queryInt(r, "from", 0, 1, int(^uint(0)>>1))
	if err != nil || from == 0 {
		http.Error(w, "from must be a positive integer", http.StatusBadRequest)
		return
	}
	to, err := queryInt(r, "to", 0, 1, int(^uint(0)>>1))
	if err != nil || to == 0 {
		http.Error(w, "to must be a positive integer", http.StatusBadRequest)
		return
	}
	diff, err := h.svc.Diff(r.Context(), promptID, from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
}

// Render 按明确的不可变版本渲染。
func (h *PromptHandler) Render(w http.ResponseWriter, r *http.Request) {
	promptID := chi.URLParam(r, "prompt_id")
	if !h.ensurePromptWorkspace(w, r, promptID) {
		return
	}
	var body struct {
		VersionID string         `json:"version_id"`
		Vars      map[string]any `json:"vars"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	version, err := h.svc.GetVersion(r.Context(), body.VersionID)
	if err != nil || version.PromptID != promptID {
		http.Error(w, "prompt version not found", http.StatusNotFound)
		return
	}
	text, err := h.svc.RenderByVersion(r.Context(), body.VersionID, body.Vars)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rendered": text})
}

// ListPrompts 列出 Prompt
// @Summary  列出 Prompt
// @Tags     prompts
// @Security BearerAuth
// @Success  200  {array}  map[string]interface{}
// @Router   /prompts [get]
func (h *PromptHandler) ListPrompts(w http.ResponseWriter, r *http.Request) {
	workspaceID := middleware.GetWorkspaceIDFromContext(r.Context())
	prompts, err := h.svc.ListPrompts(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, prompts)
}

// ListVersions 列出版本
func (h *PromptHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	promptID := chi.URLParam(r, "prompt_id")
	if !h.ensurePromptWorkspace(w, r, promptID) {
		return
	}
	versions, err := h.svc.ListVersions(r.Context(), promptID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (h *PromptHandler) ensurePromptWorkspace(w http.ResponseWriter, r *http.Request, promptID string) bool {
	if err := h.svc.EnsurePromptWorkspace(
		r.Context(), promptID, middleware.GetWorkspaceIDFromContext(r.Context()),
	); err != nil {
		http.Error(w, "prompt not found", http.StatusNotFound)
		return false
	}
	return true
}
