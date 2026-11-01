package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

type frozenCatalogTransport struct {
	pb.UnimplementedOrchestratorServer
	reads    chan string
	memoryID string
}

func (f frozenCatalogTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (f frozenCatalogTransport) ReflectMemory(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
	return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum}, nil
}

func (f frozenCatalogTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	f.reads <- first.GetUserInput().MemoryContextJson
	readArgs, _ := json.Marshal(map[string]string{"action": "read", "id": f.memoryID})
	for _, call := range []*pb.ToolRequest{
		{ToolCallId: "frozen-recall", ToolName: "RecallMemory", ParametersJson: `{"query":"prior catalog anchor","kind":"patterns"}`},
		{ToolCallId: "frozen-list", ToolName: "ManageMemory", ParametersJson: `{"action":"list"}`},
		{ToolCallId: "frozen-read", ToolName: "ManageMemory", ParametersJson: string(readArgs)},
	} {
		if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: call}}); err != nil {
			return err
		}
		response, err := stream.Recv()
		if err != nil {
			return err
		}
		f.reads <- response.GetToolResult().Output
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "Memory tools finished"}}})
}

func TestEvaluationFrozenCatalogRequiresSelectedOrigins(t *testing.T) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "RecallMemory,ManageMemory")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := config.Default(t.TempDir())
	cfg.Sandbox.Enabled = false
	cfg.RAGEnabled, cfg.OrchestratorAutoStart = false, false
	cfg.MCPConfig = "missing.json"
	workspace := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	actor, err := identity.Default().BindSession("fixture-owner")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := memory.OwnerForActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	policy := session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", MemoryRead: true, ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}
	workbench := session.NewWorkbench(ledger, nil)
	createSource := func(id string) session.FrozenMemoryReference {
		view, err := workbench.CreateWithWorkingDir(ctx, owner, "evaluation", id, "prior catalog anchor", workspace)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.BindEvaluation(ctx, ledger, view.ID, session.EvaluationBinding{MemoryNamespace: view.ID, SchemaVersion: 1, RunID: id, TaskID: id, Workspace: workspace, Model: "approved-model", Policy: policy}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		last := snapshot.Events[len(snapshot.Events)-1]
		return session.FrozenMemoryReference{SessionID: view.ID, EventID: last.EventID, Checksum: last.Checksum}
	}
	selected := createSource("selected-history")
	unselected := createSource("unselected-history")
	module := memory.NewLedgerMemory(ledger)
	encoded, err := module.Manage(ctx, unselected.SessionID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "frozen/catalog", Content: "prior catalog anchor UNSELECTED_TRIAL_ANCHOR"})
	if err != nil {
		t.Fatal(err)
	}
	var entry memory.Experience
	if err := json.Unmarshal([]byte(encoded), &entry); err != nil {
		t.Fatal(err)
	}
	var catalog session.FrozenMemoryReference
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Events) > 0 && snapshot.Events[0].Type == "memory/catalog-created" {
			last := snapshot.Events[len(snapshot.Events)-1]
			catalog = session.FrozenMemoryReference{SessionID: id, EventID: last.EventID, Checksum: last.Checksum}
		}
	}
	if catalog.SessionID == "" {
		t.Fatal("public remember did not create a catalog")
	}
	for _, includeOrigin := range []bool{false, true} {
		t.Run(map[bool]string{false: "unselected-origin", true: "selected-origin"}[includeOrigin], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			reads := make(chan string, 4)
			pb.RegisterOrchestratorServer(server, frozenCatalogTransport{reads: reads, memoryID: entry.ID})
			go server.Serve(listener)
			defer server.Stop()
			cfg.OrchestratorAddr = listener.Addr().String()
			target := filepath.Join(t.TempDir(), "clone")
			if body, err := exec.Command("git", "clone", "--no-hardlinks", workspace, target).CombinedOutput(); err != nil {
				t.Fatalf("clone fixture: %v %s", err, body)
			}
			trialPolicy := policy
			trialPolicy.FrozenMemory = []session.FrozenMemoryReference{selected, catalog}
			if includeOrigin {
				trialPolicy.FrozenMemory = append(trialPolicy.FrozenMemory, unselected)
			}
			request, _ := json.Marshal(map[string]any{"schema_version": 1, "run_id": "catalog-check", "task_id": "catalog-check", "workspace": target, "message": "Use prior catalog anchor", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 15}, "policy": trialPolicy})
			var output strings.Builder
			err = cli.RunEvaluation(ctx, cfg, strings.NewReader(string(request)), &output, io.Discard)
			if !includeOrigin {
				if err == nil {
					t.Fatal("a pinned catalog exposed an evaluation origin outside Frozen Memory refs")
				}
				if len(reads) != 0 {
					t.Fatal("an invalid Frozen Memory source reached model-facing tools")
				}
				return
			}
			if err != nil {
				t.Fatalf("selected catalog origin was rejected: %v %s", err, output.String())
			}
			for i := 0; i < 4; i++ {
				select {
				case text := <-reads:
					if !strings.Contains(text, "UNSELECTED_TRIAL_ANCHOR") {
						t.Fatalf("selected origin missing from historical read %d", i)
					}
				default:
					t.Fatal("public historical read channel was not exercised")
				}
			}
			decoder := json.NewDecoder(strings.NewReader(output.String()))
			var receipt map[string]any
			for {
				var event map[string]any
				if decoder.Decode(&event) != nil {
					break
				}
				if event["type"] == "receipt" {
					receipt = event
				}
			}
			evidence, _ := receipt["execution_evidence"].(map[string]any)
			readsProof, _ := evidence["memory_reads"].(map[string]any)
			if readsProof["automatic_read_count"] != float64(1) || readsProof["explicit_read_count"] != float64(3) || readsProof["injected_count"] != float64(1) || readsProof["returned_bytes"] == float64(0) || readsProof["injected_entries"] == float64(0) {
				t.Fatalf("on-arm lacks actual frozen Memory read/injection evidence: %+v", readsProof)
			}
			facts, err := ledger.Events(ctx, receipt["session_id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			for _, fact := range facts {
				if fact.Type != "evaluation/memory-read" && fact.Type != "evaluation/memory-injected" {
					continue
				}
				if strings.Contains(string(fact.Payload), "UNSELECTED_TRIAL_ANCHOR") {
					t.Fatal("memory content escaped into the read proof")
				}
				var proof map[string]any
				if json.Unmarshal(fact.Payload, &proof) != nil || len(proof["payload_sha256"].(string)) != 64 || len(proof["source_refs_sha256"].(string)) != 64 {
					t.Fatal("memory proof is not content-free and source-bound")
				}
			}
		})
	}
}
