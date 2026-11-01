package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

type productionChildGateTransport struct {
	evaluationTransport
	toolName  string
	arguments string
	root      chan *pb.UserInput
	child     chan *pb.UserInput
	result    chan *pb.ToolResult
	release   <-chan struct{}
}

func (f productionChildGateTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	input := message.GetUserInput()
	if input.AgentTask != nil {
		f.child <- input
		select {
		case <-f.release:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
		return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "Bounded exploration completed"}}})
	}
	if f.root != nil {
		f.root <- input
	}
	toolName := f.toolName
	if toolName == "" {
		toolName = "SpawnAgent"
	}
	if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: &pb.ToolRequest{ToolCallId: "borrowed-spawn", ToolName: toolName, ParametersJson: f.arguments}}}); err != nil {
		return err
	}
	message, err = stream.Recv()
	if err != nil {
		return err
	}
	if f.result != nil {
		f.result <- message.GetToolResult()
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "Fixture observed the bounded task result"}}})
}

func TestEvaluationProductionAgentCatalogDeclaresOnlySupportedExplore(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	result := make(chan *pb.ToolResult, 1)
	cfg, source := productionChildGateConfig(t, productionChildGateTransport{toolName: "AgentTask", arguments: `{"action":"cards"}`, result: result})
	if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(productionChildGateInput(source))), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	observed := <-result
	var cards []map[string]any
	if observed.Error != "" || json.Unmarshal([]byte(observed.Output), &cards) != nil || len(cards) != 1 || cards[0]["id"] != "explore" || !strings.Contains(cards[0]["description"].(string), "Synchronous read-only") {
		t.Fatal("production exposed an unimplemented child capability")
	}
}

func productionChildGateConfig(t *testing.T, fixture productionChildGateTransport) (config.Config, string) {
	t.Helper()
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,SpawnAgent,AgentTask,PublishArtifact")
	t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "")
	t.Setenv("CODE_AGENT_MODEL_REPORTED_MODEL", "")
	t.Setenv("CODE_AGENT_MODEL_PROVIDER_SESSION_ID", "")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, fixture)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.MCPConfig = listener.Addr().String(), false, "missing.json"
	var runtimeCalls atomic.Int64
	cfg.Sandbox = productionRuntimeFixture(t, t.TempDir(), &runtimeCalls)
	return cfg, productionRepository(t)
}

func productionChildGateInput(source string) []byte {
	input, _ := json.Marshal(map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "child-boundary", "task_id": "child-boundary", "workspace": source, "message": "Delegate bounded read-only exploration", "model": "approved-model", "lifecycle": "hold", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 30}, "policy": map[string]string{"execution_strategy": "subagent"}})
	return input
}

func TestEvaluationProductionChildRejectsUnsupportedAssignments(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	for name, arguments := range map[string]string{
		"writable kind": `{"kind":"general","title":"bounded","objective":"Read only"}`,
		"parallel":      `{"kind":"explore","title":"bounded","objective":"Read only","parallel":true}`,
		"write grant":   `{"kind":"explore","title":"bounded","objective":"Read only","allowed_tools":["Write"]}`,
		"file part":     `{"kind":"explore","title":"bounded","objective":"Read only","message":{"parts":[{"file":{"path":"result.txt"}}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := make(chan *pb.ToolResult, 1)
			cfg, source := productionChildGateConfig(t, productionChildGateTransport{arguments: arguments, result: result})
			var output strings.Builder
			if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(productionChildGateInput(source))), &output, io.Discard); err != nil {
				t.Fatalf("model did not observe the denied call: %v", err)
			}
			if (<-result).Error == "" {
				t.Fatal("unsupported production assignment was admitted")
			}
			var receipt map[string]any
			decoder := json.NewDecoder(strings.NewReader(output.String()))
			for decoder.More() {
				if decoder.Decode(&receipt) != nil {
					t.Fatal("invalid product receipt")
				}
			}
			strategy := receipt["strategy_evidence"].(map[string]any)
			if strategy["verified"] != true || len(strategy["child_session_ids"].([]any)) != 0 {
				t.Fatal("denied assignment allocated or claimed a child")
			}
		})
	}
}

func TestEvaluationProductionChildBorrowRequiresLiveParentAndCleanEffects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	for _, scenario := range []string{"live", "expired parent", "cancelled parent", "unknown write", "parent mutation", "altered link"} {
		t.Run(scenario, func(t *testing.T) {
			release := make(chan struct{})
			rootInput, childInput := make(chan *pb.UserInput, 1), make(chan *pb.UserInput, 1)
			cfg, source := productionChildGateConfig(t, productionChildGateTransport{arguments: `{"kind":"explore","title":"bounded","objective":"Read the explicit task material","parallel":false}`, root: rootInput, child: childInput, release: release})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				finished <- cli.RunEvaluation(ctx, cfg, strings.NewReader(string(productionChildGateInput(source))), io.Discard, io.Discard)
			}()
			defer func() {
				close(release)
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					cancel()
					t.Error("fixture product process failed to stop")
				}
			}()
			var parent, child *pb.UserInput
			select {
			case parent = <-rootInput:
			case <-ctx.Done():
				t.Fatal("parent was not admitted")
			}
			select {
			case child = <-childInput:
			case <-ctx.Done():
				t.Fatal("read-only child did not reach its own context")
			}
			ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			actor, _ := identity.Default().BindSession(parent.SessionId)
			owner, _ := memory.OwnerForActor(actor)
			workbench := session.NewWorkbench(ledger, nil)
			parentView, err := workbench.Get(ctx, owner, parent.SessionId)
			if err != nil || parentView.Run == nil {
				t.Fatal("parent run identity unavailable")
			}
			parentRunID := parentView.Run.RunID
			runtime := sandbox.NewOpenSandboxRunner(sandbox.Config{Backend: cfg.Sandbox.Backend, TrustRoot: cfg.Sandbox.TrustRoot, Image: cfg.Sandbox.Image, Toolchain: cfg.Sandbox.Toolchain, OpenSandboxManifest: cfg.Sandbox.OpenSandboxManifest, OpenSandboxManifestSHA256: cfg.Sandbox.OpenSandboxManifestSHA256})
			tasks := session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return owner, nil }, source, cfg.Sandbox.TrustRoot).WithBaselineRuntime(session.TaskBaselineRuntime{Toolchain: "go", Runner: runtime, Ready: runtime.Ready, Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil }})
			admitted, err := tasks.ReadOnlyAgentModelWorkspace(ctx, owner, parent.SessionId, child.SessionId)
			if err != nil || admitted.WorkingDir != child.WorkingDir || admitted.WorkingDir != parent.WorkingDir || strings.EqualFold(admitted.WorkingDir, source) || filepath.Dir(admitted.WorkingDir) != cfg.Sandbox.TrustRoot {
				t.Fatalf("child borrowed an unapproved directory: %+v %v", admitted, err)
			}
			for _, foreign := range []struct {
				owner       uint
				root, child string
			}{{owner + 1, parent.SessionId, child.SessionId}, {owner, "foreign-root", child.SessionId}, {owner, parent.SessionId, "foreign-child"}} {
				if _, err := tasks.ReadOnlyAgentModelWorkspace(ctx, foreign.owner, foreign.root, foreign.child); err == nil {
					t.Fatal("unverified child ancestry granted a workspace")
				}
			}
			appendFact := func(kind string, payload any) {
				t.Helper()
				for attempt := 0; attempt < 16; attempt++ {
					events, err := ledger.Events(ctx, parent.SessionId)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := ledger.Append(ctx, parent.SessionId, int64(len(events)), kind, payload); err == nil {
						return
					}
				}
				t.Fatal("fixture canonical append failed")
			}
			switch scenario {
			case "expired parent":
				events, _ := ledger.Events(ctx, parent.SessionId)
				for i := len(events) - 1; i >= 0; i-- {
					if events[i].Type == "session/run-leased" || events[i].Type == "session/run-heartbeat" {
						var lease map[string]any
						_ = json.Unmarshal(events[i].Payload, &lease)
						lease["lease_until"] = time.Now().Add(-time.Minute)
						appendFact("session/run-heartbeat", lease)
						break
					}
				}
			case "cancelled parent":
				appendFact("session/run-cancel-requested", map[string]any{"run_id": parentRunID, "request_id": "cancel-borrow", "actor": actor})
			case "unknown write":
				appendFact("tool/unknown", map[string]any{"run_id": parentRunID, "tool_call_id": "unconfirmed-write", "tool_name": "Write", "arguments_json": `{}`})
			case "parent mutation":
				appendFact("tool/result", map[string]any{"run_id": parentRunID, "tool_call_id": "borrowed-spawn", "tool_name": "SpawnAgent", "exit_code": 0})
				operation := sha256.Sum256([]byte("tool-operation/v1\x00" + parent.SessionId + "\x00" + parentRunID + "\x00parent-write"))
				call := map[string]any{"run_id": parentRunID, "tool_call_id": "parent-write", "tool_name": "Write", "arguments_json": `{"path":"unexpected","content":"unadmitted"}`, "operation_id": hex.EncodeToString(operation[:])}
				for _, kind := range []string{"tool/call", "commit_pending", "tool/dispatched"} {
					appendFact(kind, call)
				}
				if _, err := tasks.ToolWorkspace(ctx, owner, parent.SessionId, "parent-write"); err == nil {
					t.Fatal("parent mutation was admitted while its borrowed child was active")
				}
			case "altered link":
				appendFact("agent/task-linked", map[string]string{"task_id": child.SessionId, "child_session_id": child.SessionId, "signature": strings.Repeat("0", 64)})
			}
			_, err = tasks.ReadOnlyAgentModelWorkspace(ctx, owner, parent.SessionId, child.SessionId)
			if scenario == "live" && err != nil || scenario != "live" && err == nil {
				t.Fatalf("borrowed workspace did not enforce %s: %v", scenario, err)
			}
			if _, err := tasks.ReadOnlyAgentToolWorkspace(ctx, owner, parent.SessionId, actor, orchestrator.ToolCall{ID: "unadmitted", Name: "SpawnAgent", ParametersJSON: `{}`}); err == nil {
				t.Fatal("agent workspace admitted a call without canonical dispatch")
			}
		})
	}
}
