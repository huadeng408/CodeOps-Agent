package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"google.golang.org/grpc"
)

type evaluationContextTransport struct {
	evaluationTransport
	contexts chan string
}

func (f evaluationContextTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	f.contexts <- first.GetUserInput().EvaluationContextJson
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "Context boundary fixture completed"}}})
}

func TestEvaluationPreReadKeepsUTF8WithinPythonByteBudget(t *testing.T) {
	t.Setenv("DOCKER_CONTEXT", "codeops-file-fixture-unavailable")
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read")
	t.Setenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON", "[]")
	t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "")
	t.Setenv("CODE_AGENT_MODEL_REPORTED_MODEL", "")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	repo := filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))
	for _, tc := range []struct {
		name      string
		character string
		available int
		wantTail  string
		fill      string
	}{
		{"chinese-split-first-byte", "汉", 1, "", "a"},
		{"chinese-split-second-byte", "汉", 2, "", "a"},
		{"chinese-complete", "汉", 3, "汉", "a"},
		{"emoji-split-first-byte", "🙂", 1, "", "a"},
		{"emoji-split-second-byte", "🙂", 2, "", "a"},
		{"emoji-split-third-byte", "🙂", 3, "", "a"},
		{"emoji-complete", "🙂", 4, "🙂", "a"},
		{"ascii-complete", "Z", 1, "Z", "a"},
		{"json-html-escape", "Z", 1, "Z", "<>&"},
		{"json-control-escape", "Z", 1, "Z", "\x01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const maximum = 32000
			const readPrefix = "FILE sample.txt\n[Read sample.txt: lines 1-1 of 1]\n[Range read]\n1\t"
			padding := strings.Repeat(tc.fill, maximum)[:maximum-len(readPrefix)-tc.available]
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "sample.txt"), []byte(padding+tc.character+"tail"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
				if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git fixture: %v %s", err, body)
				}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			contexts := make(chan string, 1)
			pb.RegisterOrchestratorServer(server, evaluationContextTransport{contexts: contexts})
			go server.Serve(listener)
			defer server.Stop()
			cfg := config.Default(t.TempDir())
			cfg.Sandbox.Enabled = false
			cfg.Sandbox.WSLDistro = "codeops-file-fixture-unavailable"
			cfg.RAGEnabled, cfg.OrchestratorAutoStart = false, false
			cfg.OrchestratorAddr, cfg.MCPConfig = listener.Addr().String(), "missing.json"
			request, _ := json.Marshal(map[string]any{
				"schema_version": 1, "run_id": "utf8-boundary", "task_id": "preread", "workspace": workspace,
				"message": "Inspect sample.txt", "model": "approved-model",
				"budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 30},
				"policy": map[string]any{"context_strategy": "retrieval_pre_read", "execution_strategy": "single", "memory_read": false, "max_tool_rounds": 4, "context_max_bytes": maximum},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			var output strings.Builder
			if err := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(request)), &output, io.Discard); err != nil {
				t.Fatalf("public evaluation failed: %v %s", err, output.String())
			}
			var raw string
			select {
			case raw = <-contexts:
			default:
				t.Fatal("no evaluation context reached the transport")
			}
			payload, _ := json.Marshal(map[string]string{"raw": raw, "expected": readPrefix + padding + tc.wantTail})
			check := exec.CommandContext(ctx, python, "-c", `import json,sys
from orchestrator.server import _evaluation_context
payload=json.load(sys.stdin)
raw=payload['raw']
_evaluation_context(raw)
value=json.loads(raw)
expected=payload['expected']
assert value['context_text']==expected, (len(value['context_text'].encode('utf-8')), len(expected.encode('utf-8')))
value['context_text']+='X'*(32001-len(value['context_text'].encode('utf-8')))
try:
    _evaluation_context(json.dumps(value))
except ValueError as error:
    assert str(error)=='invalid evaluation policy'
else:
    raise AssertionError('decoded content exceeded its byte budget')
try:
    _evaluation_context(' '*256001)
except ValueError as error:
    assert str(error)=='evaluation context is too large'
else:
    raise AssertionError('raw transport limit was removed')
print(len(expected.encode('utf-8')))
`)
			check.Dir = repo
			check.Stdin = strings.NewReader(string(payload))
			check.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
			if body, err := check.CombinedOutput(); err != nil {
				t.Fatalf("actual Python rejected the bounded Go context: %v %s", err, body)
			}
		})
	}
}
