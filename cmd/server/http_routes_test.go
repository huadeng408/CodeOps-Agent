package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/service"
	"code-agent/internal/session"
	"code-agent/pkg/token"
	"github.com/gin-gonic/gin"
)

type modelTaskRouteUsers struct{ service.UserService }

func (modelTaskRouteUsers) IsTokenBlacklisted(string) (bool, error) { return false, nil }
func (modelTaskRouteUsers) GetProfile(username string) (*model.User, error) {
	owner := uint(7)
	if username == "other" {
		owner = 8
	}
	return &model.User{ID: owner, Username: username, Role: "USER"}, nil
}

func TestModelTaskAbandonRegistrationRequiresVerifiedOwner(t *testing.T) {
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(context.Background(), 7, "fixture", "canceled task", "")
	if err != nil {
		t.Fatal(err)
	}
	manager := token.NewJWTManager("route-test-secret", 1, 1)
	owner, err := manager.GenerateToken(7, "owner", "USER")
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.GenerateToken(8, "other", "USER")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	sessionHTTP{workbench: workbench}.register(router, router.Group("/api/v1"), modelTaskRouteUsers{}, manager)
	for _, scenario := range []struct {
		name   string
		token  string
		status int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"invalid", "invalid-test-token", http.StatusUnauthorized},
		{"other owner", other, http.StatusNotFound},
		{"owner reaches gateway", owner, http.StatusServiceUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+created.ID+"/task/abandon", nil)
			if scenario.token != "" {
				request.Header.Set("Authorization", "Bearer "+scenario.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != scenario.status {
				t.Fatalf("status=%d, want %d", response.Code, scenario.status)
			}
		})
	}
}
