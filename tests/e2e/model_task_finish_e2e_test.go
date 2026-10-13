package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestProductionLocalCoreFinishesOwnedTaskWithoutResettingBatch(t *testing.T) {
	fixture := startProductionLocalCore(t)
	fixture.authenticate(t)
	request := func(method, path string, payload, target any, expected int) {
		t.Helper()
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest(method, fixture.base+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response, err := fixture.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("%s %s = %d, want %d", method, path, response.StatusCode, expected)
		}
		if target != nil && json.NewDecoder(response.Body).Decode(target) != nil {
			t.Fatal("invalid product response")
		}
	}
	var profile struct {
		Data struct {
			ID                         uint
			Username, Role, PrimaryOrg string
		}
	}
	request(http.MethodGet, "/api/v1/users/me", nil, &profile, http.StatusOK)
	var created struct{ Data session.SessionView }
	request(http.MethodPost, "/api/v1/sessions", map[string]any{"projectName": "fixture", "title": "finish task"}, &created, http.StatusOK)
	actor := identity.Actor{SchemaVersion: 1, ActorID: fmt.Sprintf("user:%d", profile.Data.ID), Subject: profile.Data.Username, TenantID: profile.Data.PrimaryOrg, Roles: []string{profile.Data.Role}, SessionID: created.Data.ID}
	if actor.TenantID == "" {
		actor.TenantID = fmt.Sprintf("tenant:user:%d", profile.Data.ID)
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(fixture.dir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	var unknownUsage atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if unknownUsage.Load() {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"unattributed"}}]}`)
			return
		}
		fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	invoke := func(id string) {
		t.Helper()
		binding, release, err := gateway.BindActor(actor, actor.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		result, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: id, PayloadJson: []byte(`{"model":"fixture","messages":[{"role":"user","content":"fixture"}]}`)})
		if err != nil || result.ErrorCode != "" {
			t.Fatal("Go fixture call did not settle")
		}
	}
	invoke("call-first")
	before, err := budget.Inspect(context.Background(), profile.Data.ID)
	if err != nil || before.TaskID != created.Data.ID || before.UsedTokens != 11 {
		t.Fatal("fixture task was not held")
	}
	var capabilities struct {
		Data map[string]struct {
			Tokens struct {
				TaskID string `json:"task_id"`
			}
		}
	}
	request(http.MethodGet, "/api/v1/capabilities", nil, &capabilities, http.StatusOK)
	if capabilities.Data["budget"].Tokens.TaskID != created.Data.ID {
		t.Fatal("the owned task cannot be selected for explicit completion")
	}
	foreign, err := session.NewWorkbench(ledger, nil).Create(context.Background(), 7, "foreign", "foreign task", "")
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/api/v1/sessions/"+foreign.ID+"/task/finish", map[string]any{}, nil, http.StatusNotFound)
	unauthenticated, err := http.Post(fixture.base+"/api/v1/sessions/"+created.Data.ID+"/task/finish", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated caller finished the task")
	}
	finish := "/api/v1/sessions/" + created.Data.ID + "/task/finish"
	_, releaseScope, err := gateway.BindActor(actor, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, finish, map[string]any{}, nil, http.StatusConflict)
	releaseScope()
	request(http.MethodPost, finish, map[string]any{}, nil, http.StatusOK)
	request(http.MethodPost, finish, map[string]any{}, nil, http.StatusOK)
	fixture.stop(t)
	fixture.start(t)
	request(http.MethodPost, finish, map[string]any{}, nil, http.StatusOK)
	closed, err := budget.Inspect(context.Background(), profile.Data.ID)
	if err != nil || closed.ID != before.ID || closed.UsedTokens != 11 || closed.ReservedTokens != 0 || closed.TaskID != "" || closed.CostStatus != "unknown" {
		t.Fatal("completion or restart reset the batch")
	}
	request(http.MethodPost, "/api/v1/sessions", map[string]any{"projectName": "fixture", "title": "next task"}, &created, http.StatusOK)
	actor.SessionID = created.Data.ID
	invoke("call-second")
	request(http.MethodPost, finish, map[string]any{}, nil, http.StatusOK)
	after, err := budget.Inspect(context.Background(), profile.Data.ID)
	if err != nil || after.ID != before.ID || after.UsedTokens != 22 || after.TaskID != created.Data.ID {
		t.Fatal("the old finish action released the next task or reset its allowance")
	}
	unknownUsage.Store(true)
	binding, release, err := gateway.BindActor(actor, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "call-unknown", PayloadJson: []byte(`{"model":"fixture","messages":[{"role":"user","content":"fixture"}]}`)})
	release()
	if err != nil || result.ErrorCode != "provider_usage_unknown" {
		t.Fatal("missing usage was not retained")
	}
	request(http.MethodPost, "/api/v1/sessions/"+created.Data.ID+"/task/finish", map[string]any{}, nil, http.StatusOK)
	request(http.MethodPost, finish, map[string]any{}, nil, http.StatusOK)
	retained, err := budget.Inspect(context.Background(), profile.Data.ID)
	if err != nil || retained.ID != before.ID || retained.UsedTokens != 22 || retained.ReservedTokens != 103 || !retained.UnknownUsage || retained.TaskID != "" {
		t.Fatal("finish action cleared an unknown usage reservation")
	}
}
