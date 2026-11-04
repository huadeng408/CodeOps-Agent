package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/session"
)

func TestEvaluationCompactionProcessHelper(t *testing.T) {
	repo := os.Getenv("CODE_AGENT_COMPACTION_FIXTURE_REPO")
	if repo == "" {
		t.Skip("subprocess fixture")
	}
	cfg, err := config.Load(repo)
	if err != nil {
		os.Exit(2)
	}
	cfg.SessionDBPath = os.Getenv("CODE_AGENT_COMPACTION_FIXTURE_LEDGER")
	// This file-tool fixture must not probe the host's Docker or WSL runtime.
	cfg.Sandbox = config.SandboxConfig{}
	cfg.OrchestratorAutoStart, cfg.RAGEnabled = true, false
	cfg.OrchestratorConversationTimeout = 30
	cfg.MCPConfig = "missing.json"
	// Diagnostic PIDs belong to this fixture, outside the product Session Ledger.
	cfg.OrchestratorArgs = []string{"-c", "import os,runpy; from pathlib import Path; p=Path(os.environ['CODE_AGENT_COMPACTION_FIXTURE_PIDS']); p.open('a').write(str(os.getpid())+'\\n'); runpy.run_module('orchestrator.server',run_name='__main__')"}
	if err := cli.RunEvaluation(context.Background(), cfg, os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestEvaluationForcedCompactionPreservesUpdatedRequirementsAcrossProcesses(t *testing.T) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write")
	t.Setenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON", "[]")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "32768")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "1024")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("CODE_AGENT_EVAL_ORCHESTRATOR_STARTUP_SECONDS", "30")
	t.Setenv("CODE_AGENT_EVAL_CONTEXT_WINDOW", "500000")
	t.Setenv("CODE_AGENT_CONTEXT_BACKEND", "sqlite")
	var calls, summaries atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []map[string]any `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) == 0 {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		index := calls.Add(1)
		last := request.Messages[len(request.Messages)-1]
		text, _ := last["content"].(string)
		system, _ := request.Messages[0]["content"].(string)
		serialized, _ := json.Marshal(request.Messages)
		v2 := bytes.Contains(serialized, []byte("REQUIREMENTS_V2"))
		message := map[string]any{"role": "assistant", "content": "The requested step is complete."}
		tool, arguments := "", map[string]string{}
		if strings.HasPrefix(system, "Summarize the supplied historical records for a future agent turn.") {
			summaries.Add(1)
			message["content"] = "REQUIREMENTS_V1: blue; schema 1."
			if v2 {
				message["content"] = "REQUIREMENTS_V2: green; schema 2."
			}
		} else if last["role"] != "tool" {
			switch {
			case strings.Contains(text, "Read data/a.txt"):
				tool, arguments = "Read", map[string]string{"path": "data/a.txt"}
			case strings.Contains(text, "Read data/b.txt"):
				tool, arguments = "Read", map[string]string{"path": "data/b.txt"}
			case strings.Contains(text, "继续生成"):
				content := `{"color":"blue","schema":1}`
				if v2 {
					content = `{"color":"green","schema":2}`
				}
				tool, arguments = "Write", map[string]string{"path": "final.json", "content": content}
			}
		}
		if tool != "" {
			encoded, _ := json.Marshal(arguments)
			message["content"] = ""
			message["tool_calls"] = []map[string]any{{"id": fmt.Sprintf("fixture-call-%d", index), "type": "function", "function": map[string]string{"name": tool, "arguments": string(encoded)}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("fixture-%d", index), "model": "approved-model", "choices": []map[string]any{{"message": message, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 10}})
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	_, source, _, _ := runtime.Caller(0)
	repo := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	root, workspace := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(workspace, "data", name+".txt"), []byte("bounded public input "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init"}, {"add", "data"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	ledgerPath, pidPath := filepath.Join(root, "canonical.sqlite"), filepath.Join(root, "python-pids.txt")
	t.Setenv("CODE_AGENT_COMPACTION_FIXTURE_REPO", repo)
	t.Setenv("CODE_AGENT_COMPACTION_FIXTURE_LEDGER", ledgerPath)
	t.Setenv("CODE_AGENT_COMPACTION_FIXTURE_PIDS", pidPath)
	request := map[string]any{"schema_version": 1, "run_id": "forced-compaction-fixture", "task_id": "requirements-update", "workspace": workspace,
		"model": "approved-model", "lifecycle": "hold", "budget": map[string]any{"max_tokens": 1000000, "wall_clock_seconds": 180},
		"policy": map[string]any{"context_strategy": "metadata_on_demand", "execution_strategy": "single", "max_tool_rounds": 8, "context_max_bytes": 12000, "memory_read": false, "force_compaction": true}}
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	goPIDs := map[float64]bool{}
	for index, message := range []string{
		"REQUIREMENTS_V1: On the final step create final.json containing color blue and schema 1. Do not generate the final artifact yet; acknowledge and wait for subsequent data reading requests.",
		"REQUIREMENTS_V2 supersedes V1: the final.json artifact must use color green and schema 2. All old blue/schema 1 requirements are withdrawn. Do not generate it until the final continuation request.",
		"Read data/a.txt and acknowledge the public input.",
		"Read data/b.txt and acknowledge the public input.",
		"继续生成",
	} {
		request["message"], request["turn_id"] = message, fmt.Sprintf("turn-%d", index+1)
		input, _ := json.Marshal(request)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvaluationCompactionProcessHelper$")
		command.Stdin = bytes.NewReader(input)
		body, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture turn %d failed: %v %s", index+1, err, body)
		}
		var receipt map[string]any
		scanner := bufio.NewScanner(bytes.NewReader(body))
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event["type"] == "receipt" {
				receipt = event
			}
		}
		if receipt["task_state"] != "completed" || index > 0 && receipt["session_id"] != request["session_id"] {
			t.Fatalf("multi-process fixture left the same Session: %+v", receipt)
		}
		goPIDs[receipt["pid"].(float64)] = true
		request["session_id"], request["operation"] = receipt["session_id"], "run"
	}
	if len(goPIDs) != 5 {
		t.Fatal("sequence did not use five independent Go processes")
	}
	pythonPIDs, err := os.ReadFile(pidPath)
	if err != nil || len(strings.Fields(string(pythonPIDs))) != 5 {
		t.Fatalf("sequence did not start five real Python processes: %q %v", pythonPIDs, err)
	}
	seenPython := map[string]bool{}
	for _, pid := range strings.Fields(string(pythonPIDs)) {
		seenPython[pid] = true
	}
	if len(seenPython) != 5 {
		t.Fatal("Python process identity was reused in this fixture")
	}
	ledger, err := session.OpenReadOnlySQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, request["session_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	byID, v2ID := map[string]session.Event{}, ""
	for _, event := range snapshot.Events {
		byID[event.EventID] = event
		if event.Type == "user/message" && bytes.Contains(event.Payload, []byte("REQUIREMENTS_V2")) {
			v2ID = event.EventID
		}
	}
	coveredV2, compactions := false, 0
	compactedRuns := map[string]bool{}
	for _, event := range snapshot.Events {
		if event.Type != "context/compaction" {
			continue
		}
		var compact struct {
			RunID   string `json:"run_id"`
			InputID string `json:"input_event_id"`
			Sources []struct {
				EventID  string `json:"event_id"`
				Checksum string `json:"checksum"`
			} `json:"sources"`
		}
		if json.Unmarshal(event.Payload, &compact) != nil || compactedRuns[compact.RunID] || len(compact.Sources) == 0 {
			t.Fatal("missing sources or repeated forced compaction within one run")
		}
		compactedRuns[compact.RunID] = true
		compactions++
		for _, ref := range compact.Sources {
			original, ok := byID[ref.EventID]
			if !ok || original.Checksum != ref.Checksum || original.Seq >= event.Seq || ref.EventID == compact.InputID {
				t.Fatal("compaction did not preserve canonical source identity and active input")
			}
			coveredV2 = coveredV2 || ref.EventID == v2ID
		}
	}
	if compactions == 0 || !coveredV2 || summaries.Load() == 0 {
		t.Fatalf("no real sourced Compact RPC covered the requirement update: compactions=%d covered_v2=%v model_summaries=%d", compactions, coveredV2, summaries.Load())
	}
	final, err := os.ReadFile(filepath.Join(workspace, "final.json"))
	if err != nil || string(final) != `{"color":"green","schema":2}` {
		t.Fatalf("the last generic continuation lost V2: %q %v", final, err)
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.CommandContext(ctx, python, "-c", "import sys; from orchestrator.context import build_context_store; s=build_context_store('sqlite',project_root=sys.argv[1]); print(len(s.events(sys.argv[2]))); s.close()", workspace, request["session_id"].(string))
	check.Dir = repo
	if body, err := check.CombinedOutput(); err != nil || strings.TrimSpace(string(body)) != "0" {
		t.Fatalf("Python wrote competing Context facts: %v %s", err, body)
	}
}
