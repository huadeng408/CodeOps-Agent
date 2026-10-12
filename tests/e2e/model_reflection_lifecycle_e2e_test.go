package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	harnessorch "code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// External transports are fixtures; Runner, Memory, Gateway and Ledger are real.
func TestFailedTurnReflectionHasIndependentLifetime(t *testing.T) {
	testFailedTurnReflection(t, false)
}

func TestRunnerShutdownCancelsFailedTurnReflection(t *testing.T) {
	testFailedTurnReflection(t, true)
}

func testFailedTurnReflection(t *testing.T, shutdown bool) {
	t.Helper()
	var attempts atomic.Int64
	entered := make(chan struct{})
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		r.Body.Close()
		if attempts.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-time.After(6 * time.Second):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"fixture-model","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
		case <-r.Context().Done():
			close(canceled)
		}
	}))
	defer upstream.Close()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := grpc.NewServer()
	pb.RegisterOrchestratorServer(transport, reflectionLifetimeTransport{})
	go transport.Serve(listener)
	defer transport.Stop()
	client, err := harnessorch.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetModelScopeFactory(gateway.BindActor)
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(context.Background(), 7, "fixture", "failed turn reflection", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner := session.NewSessionRunner(workbench, client, nil, session.SessionRunnerOptions{WorkerID: "reflection-lifetime", Memory: memory.NewLedgerMemory(ledger, client.ReflectMemory)})
	defer runner.Close()
	run, err := runner.SubmitMessage(context.Background(), session.SubmitMessageCommand{RequestID: "failed-turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "inspect the fixture", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	if shutdown {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("reflection never reached the upstream")
		}
		closed := make(chan error, 1)
		go func() { closed <- runner.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("runner shutdown waited for detached reflection")
		}
		select {
		case <-canceled:
		case <-time.After(2 * time.Second):
			t.Fatal("runner shutdown left upstream request alive")
		}
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		batch, err := budget.Inspect(context.Background(), 7)
		if err != nil {
			t.Fatal(err)
		}
		if batch.UnknownUsage {
			if shutdown && batch.UsedTokens == 0 && batch.ReservedTokens == 103 {
				current, err := runner.Run(context.Background(), created.ID, run.RunID)
				if err != nil || current.Status != session.RunFailed || attempts.Load() != 1 {
					t.Fatal("shutdown changed terminal failure or dispatched twice")
				}
				return
			}
			t.Fatalf("failed-turn persistence deadline canceled the six-second reflection: used=%d reserved=%d", batch.UsedTokens, batch.ReservedTokens)
		}
		if batch.UsedTokens == 5 && batch.ReservedTokens == 0 {
			if shutdown {
				t.Fatal("shutdown did not retain unknown usage")
			}
			current, err := runner.Run(context.Background(), created.ID, run.RunID)
			if err != nil || current.Status != session.RunFailed || attempts.Load() != 1 {
				t.Fatal("reflection changed terminal failure or dispatched twice")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("bounded reflection did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type reflectionLifetimeTransport struct {
	pb.UnimplementedOrchestratorServer
}

func (reflectionLifetimeTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (reflectionLifetimeTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: false, ErrorCode: "fixture_runtime_failure"}}})
}

func (reflectionLifetimeTransport) ReflectMemory(ctx context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
	binding := request.GetModelGateway()
	if binding == nil {
		return nil, status.Error(codes.FailedPrecondition, "model binding required")
	}
	connection, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	response, err := pb.NewModelGatewayClient(connection).Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "fixture-reflection", Purpose: "memory_reflection", PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"Summarize this failed fixture turn."}]}`)})
	if err != nil || response.ErrorCode != "" {
		return nil, status.Error(codes.Unavailable, "reflection transport unavailable")
	}
	return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum}, nil
}
