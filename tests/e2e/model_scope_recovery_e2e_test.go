package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestProductionLocalCoreRecoversStoppedModelScopeHolder(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown_usage_%t", unknown), func(t *testing.T) {
			fixture := startProductionLocalCore(t)
			fixture.client.Timeout = 10 * time.Second
			fixture.authenticate(t)
			request := func(method, path string, payload, target any, expected int) {
				t.Helper()
				body, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequest(method, fixture.base+path, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
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
			request(http.MethodPost, "/api/v1/sessions", map[string]any{"projectName": "fixture", "title": "stopped holder"}, &created, http.StatusOK)
			actor := identity.Actor{SchemaVersion: 1, ActorID: fmt.Sprintf("user:%d", profile.Data.ID), Subject: profile.Data.Username,
				TenantID: profile.Data.PrimaryOrg, Roles: []string{profile.Data.Role}, SessionID: created.Data.ID}
			if actor.TenantID == "" {
				actor.TenantID = fmt.Sprintf("tenant:user:%d", profile.Data.ID)
			}
			actorJSON, err := json.Marshal(actor)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(fixture.dir, "sessions.sqlite")
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelScopeHTTPProcessHelper$", "-test.v")
			command.Env = append(os.Environ(), "CODE_AGENT_SCOPE_HTTP_DB="+path, "CODE_AGENT_SCOPE_HTTP_ACTOR="+string(actorJSON),
				fmt.Sprintf("CODE_AGENT_SCOPE_HTTP_UNKNOWN=%t", unknown))
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
				_ = input.Close()
			})
			scanner := bufio.NewScanner(output)
			ready := false
			for scanner.Scan() {
				if scanner.Text() == "SCOPE_HTTP_READY" {
					ready = true
					break
				}
			}
			if !ready {
				t.Fatal("test-owned HTTP scope process did not become ready")
			}
			ledger, err := session.OpenSQLiteEventLog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			before, err := budget.Inspect(ctx, profile.Data.ID)
			if err != nil || before.ID == "" || before.TaskID != created.Data.ID {
				t.Fatal("independent scope holder did not acquire its owned task")
			}
			finish := "/api/v1/sessions/" + created.Data.ID + "/task/finish"
			request(http.MethodPost, finish, map[string]any{}, nil, http.StatusConflict)
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			fixture.stop(t)
			fixture.start(t)
			expected := http.StatusOK
			if unknown {
				expected = http.StatusConflict
			}
			request(http.MethodPost, finish, map[string]any{}, nil, expected)
			request(http.MethodPost, finish, map[string]any{}, nil, expected)
			after, err := budget.Inspect(ctx, profile.Data.ID)
			if err != nil || after.ID != before.ID || after.CostStatus != "unknown" {
				t.Fatal("HTTP recovery replaced the batch or invented a monetary cost")
			}
			if unknown {
				if after != before || after.UsedTokens != 0 || after.ReservedTokens != 103 || !after.UnknownUsage {
					t.Fatal("holder exit or HTTP restart cleared unknown usage")
				}
			} else if after.UsedTokens != 11 || after.ReservedTokens != 0 || after.TaskID != "" || after.UnknownUsage {
				t.Fatal("HTTP recovery changed settled usage or retained the stopped holder")
			}
		})
	}
}

func TestModelScopeHTTPProcessHelper(t *testing.T) {
	path := os.Getenv("CODE_AGENT_SCOPE_HTTP_DB")
	if path == "" {
		t.Skip("test-owned HTTP scope subprocess helper")
	}
	var actor identity.Actor
	if json.Unmarshal([]byte(os.Getenv("CODE_AGENT_SCOPE_HTTP_ACTOR")), &actor) != nil {
		t.Fatal("invalid fixture actor")
	}
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	unknown := os.Getenv("CODE_AGENT_SCOPE_HTTP_UNKNOWN") == "true"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if unknown {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"unattributed"}}]}`)
			return
		}
		fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.BindActor(actor, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "http-scope-call",
		PayloadJson: []byte(`{"model":"fixture","messages":[{"role":"user","content":"fixture"}]}`)})
	expected := ""
	if unknown {
		expected = "provider_usage_unknown"
	}
	if err != nil || result.ErrorCode != expected {
		t.Fatal("independent HTTP fixture call did not settle")
	}
	fmt.Println("SCOPE_HTTP_READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
