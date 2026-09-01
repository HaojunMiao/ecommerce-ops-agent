package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
)

func TestRegisterRequiresAuthentication(t *testing.T) {
	services := platform.NewService(nil, nil, []byte("test-jwt-key-at-least-32-characters"), nil, nil)
	t.Cleanup(services.Audit.Close)
	routes := NewHandler(services, nil, nil).Routes()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{
		"email":"user@example.com","password":"password","name":"User"
	}`))
	request.Header.Set("Content-Type", "application/json")
	routes.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
