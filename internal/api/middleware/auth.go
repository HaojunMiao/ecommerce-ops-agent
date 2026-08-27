package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/iam"
)

type userIDKey struct{}
type workspaceIDKey struct{}
type workspaceRoleKey struct{}

// 读取 Authorization: Bearer <token>，解析 JWT，将 userID 写入请求上下文。
func Auth(service *iam.Service) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if raw == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			userID, err := service.ParseToken(raw)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey{}, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Workspace：读取 X-Workspace-ID，查询用户的真实成员角色并检查操作权限。
func Workspace(service *iam.Service) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
			if workspaceID == "" {
				http.Error(w, "workspace is required", http.StatusBadRequest)
				return
			}
			if service == nil {
				http.Error(w, "workspace access denied", http.StatusForbidden)
				return
			}
			role, err := service.WorkspaceRole(r.Context(), UserID(r.Context()), workspaceID)
			if err != nil || !workspaceMethodAllowed(role, r.Method, r.URL.Path) {
				http.Error(w, "workspace access denied", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), workspaceIDKey{}, workspaceID)
			ctx = context.WithValue(ctx, workspaceRoleKey{}, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID、WorkspaceID、WorkspaceRole：从 context 读取中间件写入的信息。
func UserID(ctx context.Context) string {
	value, _ := ctx.Value(userIDKey{}).(string)
	return value
}

func WorkspaceID(ctx context.Context) string {
	value, _ := ctx.Value(workspaceIDKey{}).(string)
	return value
}

func WorkspaceRole(ctx context.Context) string {
	value, _ := ctx.Value(workspaceRoleKey{}).(string)
	return value
}

// workspaceMethodAllow viewer 只读，member 可运行已发布能力，
// editor 可修改控制面，审批动作只允许 owner/admin。
func workspaceMethodAllowed(role, method, path string) bool {
	if method != http.MethodGet && method != http.MethodHead &&
		(strings.Contains(path, "/approvals/") || strings.HasSuffix(path, "/a2ui/actions")) {
		return role == iam.WorkspaceRoleOwner || role == iam.WorkspaceRoleAdmin
	}
	if role == iam.WorkspaceRoleOwner || role == iam.WorkspaceRoleAdmin || role == iam.WorkspaceRoleEditor {
		return true
	}
	if method == http.MethodGet || method == http.MethodHead {
		return true
	}
	if role == iam.WorkspaceRoleViewer {
		return false
	}
	return strings.HasSuffix(path, "/chat") ||
		strings.HasSuffix(path, "/search") ||
		strings.HasSuffix(path, "/runs") ||
		strings.Contains(path, "/stream/")
}
