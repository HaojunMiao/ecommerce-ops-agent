package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/api/middleware"
	markdown "github.com/HaojunMiao/ecommerce-ops-agent/internal/connector/markdown_folder"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/iam"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/kb"
	platformtool "github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/tool"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
)

type ControlPlane struct {
	Tools  *platformtool.Registry
	KBs    *kb.Service
	Search *retriever.KnowledgeSearch
}

func NewRouter(iamService *iam.Service, runtimes ...ChatRuntime) http.Handler {
	return NewRouterWithControlPlane(iamService, firstRuntime(runtimes), ControlPlane{})
}

func NewRouterWithControlPlane(iamService *iam.Service, runtime ChatRuntime, control ControlPlane) http.Handler {
	// 使用chi框架
	router := chi.NewRouter()

	// 注册中间件
	// RequestID中间件用于给一次HTTP请求分配唯一编号。写入响应头X-Request-ID、放进请求的context
	router.Use(middleware.Recoverer, middleware.RequestID)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 平台相关的一些API（注册、登录、列出用户有权限的workspace）
	// 注册
	router.Post("/api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		// 从请求中拿出用户注册相关的数据，入库
		var req struct{ Email, Password, Name string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// 注册（入库）
		user, err := iamService.Register(r.Context(), req.Email, req.Password, req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, user)
	})
	// 登录
	router.Post("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Email, Password string }
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// 登录
		result, err := iamService.Login(r.Context(), req.Email, req.Password)
		if err != nil {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	router.Group(func(protected chi.Router) {
		// auth中间件：从请求头拿到jwt，解析出userID
		protected.Use(middleware.Auth(iamService))

		// 列出有权限的workspace
		protected.Get("/api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
			workspaces, err := iamService.ListUserWorkspaces(r.Context(), middleware.UserID(r.Context()))
			if err != nil {
				http.Error(w, "list workspaces", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, workspaces)
		})
		protected.With(middleware.Workspace(iamService)).Get("/api/v1/context", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"user_id":      middleware.UserID(r.Context()),
				"workspace_id": middleware.WorkspaceID(r.Context()),
				"role":         middleware.WorkspaceRole(r.Context()),
			})
		})
		if control.Tools != nil {
			protected.With(middleware.Workspace(iamService)).Get("/api/v1/tools", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, control.Tools.List(r.Context(), middleware.WorkspaceID(r.Context())))
			})
			protected.With(middleware.Workspace(iamService)).Post("/api/v1/tools", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Name           string `json:"name"`
					SourceType     string `json:"source_type"`
					Description    string `json:"description"`
					SchemaJSON     string `json:"schema_json"`
					EndpointConfig string `json:"endpoint_config"`
					AuthConfig     string `json:"auth_config"`
					Sensitive      bool   `json:"sensitive"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				var endpoint struct {
					URL     string `json:"url"`
					SDKName string `json:"sdk_name"`
				}
				if err := json.Unmarshal([]byte(req.EndpointConfig), &endpoint); err != nil {
					http.Error(w, "invalid endpoint_config", http.StatusBadRequest)
					return
				}
				versionID := fmt.Sprintf("tool-version-%d", time.Now().UnixNano())
				// REST 工具保存 URL；internal_sdk 工具保存进程内注册名。
				executableEndpoint := endpoint.URL
				if req.SourceType == "internal_sdk" {
					executableEndpoint = endpoint.SDKName
				}
				version := platformtool.Version{
					ID: versionID, ToolID: versionID,
					WorkspaceID: middleware.WorkspaceID(r.Context()), Name: req.Name,
					SourceType: req.SourceType, Description: req.Description,
					InputSchema: []byte(req.SchemaJSON), Endpoint: executableEndpoint,
					AuthConfig: req.AuthConfig, HasAuth: req.AuthConfig != "",
					Sensitive: req.Sensitive, Published: true, CreatedAt: time.Now().UTC(),
				}
				if err := control.Tools.Register(r.Context(), version); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusCreated, version)
			})
		}
		if control.KBs != nil {
			// 知识库属于工作空间资源，所有接口都必须经过 Workspace 中间件校验。
			protected.With(middleware.Workspace(iamService)).Get("/api/v1/kbs", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, control.KBs.List(r.Context(), middleware.WorkspaceID(r.Context())))
			})
			protected.With(middleware.Workspace(iamService)).Post("/api/v1/kbs", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Name string `json:"name"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				base, err := control.KBs.Create(r.Context(), middleware.WorkspaceID(r.Context()), req.Name)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeJSON(w, http.StatusCreated, base)
			})
			protected.With(middleware.Workspace(iamService)).Post("/api/v1/kbs/{kbID}/connectors/markdown/sync", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					RootPath string `json:"root_path"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				// 每次同步请求创建一个带本次 RootPath 的 Connector 实例。
				job, err := control.KBs.Sync(
					r.Context(), middleware.WorkspaceID(r.Context()), chi.URLParam(r, "kbID"),
					markdown.New(req.RootPath),
				)
				if err != nil {
					// 返回失败的 job，调用方仍能看到具体失败阶段和原因。
					writeJSON(w, http.StatusUnprocessableEntity, job)
					return
				}
				writeJSON(w, http.StatusAccepted, job)
			})
			protected.With(middleware.Workspace(iamService)).Get("/api/v1/kbs/{kbID}/documents", func(w http.ResponseWriter, r *http.Request) {
				documents, err := control.KBs.Documents(
					r.Context(), middleware.WorkspaceID(r.Context()), chi.URLParam(r, "kbID"),
				)
				if err != nil {
					http.Error(w, "knowledge base not found", http.StatusNotFound)
					return
				}
				writeJSON(w, http.StatusOK, documents)
			})
			if control.Search != nil {
				// 管理端检索 Playground：不经过模型，便于直接比较三种检索模式。
				protected.With(middleware.Workspace(iamService)).Post("/api/v1/kbs/{kbID}/search", func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						Query string `json:"query"`
						Mode  string `json:"mode"`
						TopK  int    `json:"top_k"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						http.Error(w, "invalid JSON", http.StatusBadRequest)
						return
					}
					if req.TopK == 0 {
						req.TopK = 5
					}
					results, err := control.Search.Search(
						r.Context(), middleware.WorkspaceID(r.Context()), chi.URLParam(r, "kbID"),
						req.Query, req.Mode, req.TopK,
					)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					writeJSON(w, http.StatusOK, results)
				})
			}
		}
		if runtime != nil {
			protected.With(middleware.Workspace(iamService)).Post("/stream/agents/{agentID}/chat", NewStreamHandler(runtime).ServeHTTP)
		}
	})
	return router
}

func firstRuntime(runtimes []ChatRuntime) ChatRuntime {
	if len(runtimes) == 0 {
		return nil
	}
	return runtimes[0]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
