package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Only the external orchestrator transport is a fixture; CLI and Ledger are real.
type hookTransport struct {
	codeagentpb.UnimplementedOrchestratorServer
	IDs []string
}

type admissionTransport struct {
	codeagentpb.UnimplementedOrchestratorServer
	observed chan string
}

func (admissionTransport) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok"}, nil
}

func (fixture admissionTransport) Converse(stream grpc.BidiStreamingServer[codeagentpb.HarnessMessage, codeagentpb.OrchestratorMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	binding := message.GetUserInput().GetModelGateway()
	code := "binding_missing"
	if binding != nil {
		connection, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}
		defer connection.Close()
		response, err := codeagentpb.NewModelGatewayClient(connection).Invoke(stream.Context(), &codeagentpb.ModelCallRequest{
			Capability: binding.Capability, CallId: "missing-credentials", PayloadJson: []byte(`{"model":"fixture","messages":[{"role":"user","content":"verify admission"}]}`),
		})
		if err != nil {
			return err
		}
		code = response.ErrorCode
	}
	fixture.observed <- code
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{ErrorCode: code, Message: "model prerequisite refusal"}}})
}

func (fixture admissionTransport) Compact(_ context.Context, request *codeagentpb.CompactRequest) (*codeagentpb.CompactionUpdate, error) {
	code := "binding_missing"
	if request.ModelGateway != nil {
		code = "bound"
	}
	fixture.observed <- code
	return &codeagentpb.CompactionUpdate{}, nil
}

func TestCLIRestartCompactionIsBoundBeforeForegroundInput(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 1)
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, admissionTransport{observed: observed})
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = listener.Addr().String(), false, false
	manager := session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
	previous := manager.NewSession(cfg.WorkingDir)
	actor, err := identity.Default().BindSession(previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetActor(actor)
	manager.Append(session.RoleUser, "retained input")
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cli.NewApp(cfg, strings.NewReader("/resume "+previous.ID+"\n/compact\n"), io.Discard, io.Discard).Run(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-observed:
		if code != "bound" {
			t.Fatalf("restarted compaction bypassed default admission: %s", code)
		}
	default:
		t.Fatal("restored session did not reach the compaction transport")
	}
}

func TestCLIBudgetBeforeForegroundInputShowsRetainedReservation(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = "127.0.0.1:1", false, false
	store := session.NewSQLiteEventStore(cfg.SessionDBPath)
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	batch, err := budget.Reserve(context.Background(), 7, "retained-task", "interrupted-call", 103)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.MarkUnknown(context.Background(), 7, "interrupted-call"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cli.NewApp(cfg, strings.NewReader("/budget\n/config\n"), &output, io.Discard).Run(ctx); err != nil {
		t.Fatal(err)
	}
	normalized := strings.Join(strings.Fields(output.String()), " ")
	for _, expected := range []string{batch.ID, "0 used / 103 reserved / 100000000 limit", "usage unknown: true", "no monetary cap"} {
		if !strings.Contains(normalized, expected) {
			t.Fatalf("startup hid the retained token projection: missing %q", expected)
		}
	}
	if strings.Contains(output.String(), "max cost per session:") {
		t.Fatal("startup advertised the replaced per-session money cap")
	}
}

func TestCLIRestartClearRetainsUnknownModelReservation(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = "127.0.0.1:1", false, false
	store := session.NewSQLiteEventStore(cfg.SessionDBPath)
	manager := session.NewManager(store)
	previous := manager.NewSession(cfg.WorkingDir)
	actor, err := identity.Default().BindSession(previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetActor(actor)
	manager.Append(session.RoleUser, "retained interrupted input")
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	binding, release, err := gateway.BindActor(actor, previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := gateway.Invoke(context.Background(), &codeagentpb.ModelCallRequest{Capability: binding.Capability,
		CallId: "interrupted-call", PayloadJson: []byte(`{"model":"fixture","messages":[{"role":"user","content":"retained interrupted input"}]}`)})
	if err != nil || response.ErrorCode != "provider_usage_unknown" {
		t.Fatalf("fixture did not retain unknown usage: %v %v", response, err)
	}
	release()
	gateway.Close()
	before, err := budget.Inspect(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cli.NewApp(cfg, strings.NewReader("/resume "+previous.ID+"\n/clear\n"), &output, io.Discard).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "session cleared") || strings.Contains(output.String(), "task cannot finish") {
		t.Fatal("terminal unknown usage prevented clearing a stopped task")
	}
	reopened := session.NewSQLiteEventStore(cfg.SessionDBPath)
	defer reopened.Close()
	ledger, err = reopened.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	after, err := admission.NewBudget(ledger).Inspect(context.Background(), 7)
	if err != nil || after.ID != before.ID || after.TaskID != "" || !after.UnknownUsage || after.ReservedTokens != 103 || after.UsedTokens != 0 {
		t.Fatalf("clear/reset erased the interrupted reservation: %+v %v", after, err)
	}
}

func TestCLIDefaultAdmissionRetainsInputWhenCredentialsAreMissing(t *testing.T) {
	for name, value := range map[string]string{
		"CODE_AGENT_MODEL_ADMISSION": "", "LLM_PROVIDER": "openai", "OPENAI_API_KEY": "",
		"OPENAI_MODEL": "fixture", "OPENAI_BASE_URL": "https://example.invalid/v1",
		"CODE_AGENT_MODEL_INPUT_LIMIT": "100", "CODE_AGENT_MODEL_OUTPUT_LIMIT": "3", "OTEL_SDK_DISABLED": "true",
	} {
		t.Setenv(name, value)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 1)
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, admissionTransport{observed: observed})
	go server.Serve(listener)
	defer server.Stop()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = listener.Addr().String(), false, false
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cli.NewApp(cfg, strings.NewReader("verify admission\n"), io.Discard, io.Discard).Run(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-observed:
		if code != "model_prerequisites_missing" {
			t.Fatalf("default product entry bypassed admission: %s", code)
		}
	default:
		t.Fatal("orchestrator did not receive the retained input")
	}
	store := session.NewSQLiteEventStore(cfg.SessionDBPath)
	manager := session.NewManager(store)
	defer manager.Close()
	sessions, err := manager.List(context.Background())
	if err != nil || len(sessions) != 1 || len(sessions[0].Messages) == 0 || sessions[0].Messages[0].Content != "verify admission" {
		t.Fatalf("admission refusal lost user input: %+v %v", sessions, err)
	}
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := admission.NewBudget(ledger).Inspect(context.Background(), 1)
	if err != nil || batch.ID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
		t.Fatalf("missing credentials created billing facts: %+v %v", batch, err)
	}
}

func (hookTransport) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok"}, nil
}

func (fixture hookTransport) Converse(stream grpc.BidiStreamingServer[codeagentpb.HarnessMessage, codeagentpb.OrchestratorMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	ids := fixture.IDs
	if ids == nil {
		ids = []string{"blocked-hook", "blocked-hook"}
	}
	for _, id := range ids {
		if err := stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_ToolRequest{ToolRequest: &codeagentpb.ToolRequest{ToolCallId: id, ToolName: "Write", ParametersJson: `{"path":"tool-marker.txt","content":"must not execute"}`}}}); err != nil {
			return err
		}
		result, err := stream.Recv()
		if err != nil {
			return err
		}
		if result.GetToolResult() == nil || result.GetToolResult().Error == "" {
			return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Message: "unexpected tool success"}}})
		}
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Message: "expected boundary refusal"}}})
}

func TestCLIRejectsMissingInvocationIdentityBeforeEffects(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, hookTransport{IDs: []string{"", " \t "}})
	go server.Serve(listener)
	defer server.Stop()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.WorkingDir = root
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = listener.Addr().String(), false, false
	cfg.Sandbox = config.SandboxConfig{Backend: sandbox.BackendOpenSandbox, TrustRoot: root}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app := cli.NewApp(cfg, strings.NewReader("verify identity\ny\ny\n"), io.Discard, io.Discard)
	if err := app.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tool-marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("missing identity bypassed durable intent before Write: %v", err)
	}
	manager := session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
	defer manager.Close()
	sessions, err := manager.List(context.Background())
	if err != nil || len(sessions) != 1 || len(sessions[0].Invocations) != 0 {
		t.Fatalf("missing identity fabricated an invocation: %+v %v", sessions, err)
	}
}

func TestCLIRefusesUnapprovedHookAndPersistsItsInvocation(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, hookTransport{})
	go server.Serve(listener)
	defer server.Stop()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart = listener.Addr().String(), false
	cfg.RAGEnabled = false
	cfg.Sandbox = config.SandboxConfig{Backend: sandbox.BackendOpenSandbox, TrustRoot: root}
	cfg.Hooks = []config.HookConfig{{Type: "pre_tool", Matcher: "Write", Command: "echo escaped > hook-marker.txt"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app := cli.NewApp(cfg, strings.NewReader("verify boundary\ny\n"), io.Discard, io.Discard)
	if err := app.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hook-marker.txt", "tool-marker.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("selected boundary was bypassed by %s: %v", name, err)
		}
	}
	manager := session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
	defer manager.Close()
	sessions, err := manager.List(context.Background())
	if err != nil || len(sessions) != 1 || len(sessions[0].Invocations) != 1 {
		t.Fatalf("CLI lost or duplicated its blocked invocation: %+v %v", sessions, err)
	}
	invocation := sessions[0].Invocations[0]
	encoded, _ := json.Marshal(invocation)
	var identity struct {
		OperationID string `json:"operation_id"`
	}
	if json.Unmarshal(encoded, &identity) != nil || invocation.ID != "blocked-hook" || len(identity.OperationID) != 64 || invocation.Status != session.InvocationCommitted || invocation.Error == "" || invocation.ExitCode == 0 {
		t.Fatalf("CLI did not retain the selected boundary refusal: %+v", invocation)
	}
}
