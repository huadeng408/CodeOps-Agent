package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

func TestEvaluationProductionRequiresApprovedRuntimeBeforeBootstrap(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	workspace := productionRepository(t)
	input, _ := json.Marshal(map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "required-runtime", "task_id": "required-runtime", "workspace": workspace, "message": "Perform the task", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 30}})
	var output strings.Builder
	if cli.RunEvaluation(context.Background(), config.Default(t.TempDir()), strings.NewReader(string(input)), &output, io.Discard) == nil || !strings.Contains(output.String(), `"error_code":"production_runtime_required"`) {
		t.Fatalf("production request did not fail closed before bootstrap: %s", output.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, ".agent")); !os.IsNotExist(err) {
		t.Fatal("refused production request modified the source repository")
	}
}

func TestEvaluationProductionPreparesCanonicalWorkspaceAndExecutesOpenSandbox(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses the Windows native pin contract")
	}
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Bash")
	t.Setenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON", `["go test ./... -count=1"]`)
	t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "")
	t.Setenv("CODE_AGENT_MODEL_REPORTED_MODEL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	var modelCalls, runtimeCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		io.WriteString(w, `{"model":"approved-model","usage":{"prompt_tokens":100,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	workspace, storage := productionRepository(t), t.TempDir()
	var polluteBootstrap atomic.Bool
	polluteBootstrap.Store(true)
	pb.RegisterOrchestratorServer(server, evaluationTransport{bashAfterWrite: true, onHealth: func() {
		if !polluteBootstrap.Load() {
			return
		}
		entries, err := os.ReadDir(storage)
		if err != nil {
			t.Error(err)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "lease-") {
				directory := filepath.Join(storage, entry.Name(), ".agent")
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Error(err)
					return
				}
				if err := os.WriteFile(filepath.Join(directory, "fixture-bootstrap.sqlite"), []byte("unadmitted runtime state"), 0600); err != nil {
					t.Error(err)
				}
			}
		}
	}})
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.MCPConfig = listener.Addr().String(), false, "missing.json"
	cfg.Sandbox = productionRuntimeFixture(t, storage, &runtimeCalls)
	request := map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "production-fixture", "task_id": "write-and-check", "workspace": workspace, "message": "Write result.txt and run the public structural check", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 45}}
	badConfig := cfg
	badConfig.Sandbox.OpenSandboxManifestSHA256 = strings.Repeat("9", 64)
	badInput, _ := json.Marshal(request)
	var refused strings.Builder
	if cli.RunEvaluation(context.Background(), badConfig, strings.NewReader(string(badInput)), &refused, io.Discard) == nil || !strings.Contains(refused.String(), `"error_code":"production_runtime_unavailable"`) || modelCalls.Load() != 0 || runtimeCalls.Load() != 0 {
		t.Fatal("bad runtime pin reached a model or task")
	}
	ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := identity.Default().BindSession("evaluation-owner")
	owner, _ := memory.OwnerForActor(actor)
	unprepared, err := session.NewWorkbench(ledger, nil).CreateWithWorkingDir(context.Background(), owner, "source", "unprepared", "", workspace)
	if err != nil {
		t.Fatal(err)
	}
	_ = ledger.Close()
	request["session_id"], request["operation"] = unprepared.ID, "inspect"
	badInput, _ = json.Marshal(request)
	refused.Reset()
	if cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(badInput)), &refused, io.Discard) == nil || !strings.Contains(refused.String(), `"error_code":"production_workspace_unavailable"`) || modelCalls.Load() != 0 || runtimeCalls.Load() != 0 {
		t.Fatal("unprepared Session bypassed the product workspace gate")
	}
	delete(request, "session_id")
	delete(request, "operation")
	badInput, _ = json.Marshal(request)
	refused.Reset()
	if cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(badInput)), &refused, io.Discard) == nil {
		t.Fatal("unadmitted bootstrap state was ignored")
	}
	var failure map[string]any
	if json.NewDecoder(strings.NewReader(refused.String())).Decode(&failure) != nil || failure["session_id"] == "" || failure["error_code"] != "production_workspace_integrity_unconfirmed" || failure["task_state"] != "infra_error" {
		t.Fatalf("prepared failure lost its identity or category: %s", refused.String())
	}
	if failure["ledger"].(map[string]any)["verified"] != true || failure["task_workspace"].(map[string]any)["admission_ready"] != false || modelCalls.Load() != 0 || runtimeCalls.Load() != 0 {
		t.Fatal("failed workspace admission fabricated model dispatch or discarded its evidence")
	}
	polluteBootstrap.Store(false)
	call := func() map[string]any {
		input, _ := json.Marshal(request)
		var output strings.Builder
		if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(input)), &output, io.Discard); err != nil {
			t.Fatalf("production entry failed: %v %s", err, output.String())
		}
		var receipt map[string]any
		decoder := json.NewDecoder(strings.NewReader(output.String()))
		for {
			var event map[string]any
			if decoder.Decode(&event) != nil {
				break
			}
			if event["type"] == "receipt" {
				receipt = event
			}
		}
		return receipt
	}
	receipt := call()
	actual, ok := receipt["workspace"].(string)
	if !ok || actual == workspace || receipt["source_workspace"] != workspace || receipt["runtime_contract"] != "production_opensandbox" || receipt["product_path"] != "opensandbox-production" {
		t.Fatal("receipt did not distinguish the prepared product workspace")
	}
	prepared := receipt["task_workspace"].(map[string]any)
	if prepared["verified"] != true || prepared["admission_ready"] != true || prepared["approved_root"] != storage || prepared["working_dir"] != actual || actual != filepath.Join(storage, prepared["workspace_id"].(string)) || prepared["lease_id"] != prepared["workspace_id"] || prepared["prepared_event_id"] == "" || prepared["prepared_event_checksum"] == "" || prepared["baseline_checksum"] == "" {
		t.Fatalf("workspace evidence is incomplete: %+v", prepared)
	}
	if body, err := os.ReadFile(filepath.Join(actual, "result.txt")); err != nil || string(body) != "product tool completed\n" {
		t.Fatal("Go did not write the approved prepared workspace")
	}
	if _, err := os.Stat(filepath.Join(workspace, "result.txt")); !os.IsNotExist(err) {
		t.Fatal("model tool changed the source repository")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".agent")); !os.IsNotExist(err) {
		t.Fatal("bootstrap wrote manifests to the source repository")
	}
	proof := receipt["execution_evidence"].(map[string]any)
	found := false
	for _, raw := range proof["tools"].([]any) {
		item := raw.(map[string]any)
		if item["tool_name"] == "Bash" {
			found = true
			if item["runtime_id"] == "" || item["stopped"] != true || item["outcome_unknown"] != false || item["result_event_id"] == "" || item["result_event_checksum"] == "" {
				t.Fatal("OSB execution and stop proof was omitted")
			}
		}
	}
	if !found || runtimeCalls.Load() != 1 || modelCalls.Load() != 1 {
		t.Fatal("configured pins were substituted for an actual runtime operation")
	}
	request["session_id"], request["operation"] = receipt["session_id"], "inspect"
	inspected := call()
	if inspected["workspace"] != actual || inspected["ledger"].(map[string]any)["head_checksum"] != receipt["ledger"].(map[string]any)["head_checksum"] || modelCalls.Load() != 1 || runtimeCalls.Load() != 1 {
		t.Fatal("inspect re-prepared or re-executed the production task")
	}
}

func productionRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	return root
}

func productionRuntimeFixture(t *testing.T, storage string, calls *atomic.Int64, graderOutput ...func(string) string) config.SandboxConfig {
	t.Helper()
	serverID, taskID, sidecarID := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
	serverImage, taskImage, execdImage, egressImage := "sha256:"+strings.Repeat("4", 64), "sha256:"+strings.Repeat("5", 64), "sha256:"+strings.Repeat("6", 64), "sha256:"+strings.Repeat("7", 64)
	source, policy, sandboxID := "f89eff24bbd15ae1c37a47769f7667849e679be0", strings.Repeat("8", 64), "11111111-2222-4333-8444-555555555555"
	hostPath := func(path string) string {
		return "/run/desktop/mnt/host/" + strings.ToLower(path[:1]) + strings.ReplaceAll(path[2:], `\`, "/")
	}
	objects := map[string]any{}
	for _, image := range []string{serverImage, taskImage, execdImage, egressImage} {
		objects[image] = map[string]any{"Id": image, "Config": map[string]any{"Env": []string{"PATH=/usr/local/bin:/usr/bin"}, "Labels": map[string]string{"codeops.docker.project": "localcode", "codeops.opensandbox.lease-seconds": "630"}}}
	}
	objects[serverID] = map[string]any{"Id": serverID, "Config": map[string]any{"Image": serverImage, "WorkingDir": "/app", "Env": []string{"PATH=/usr/local/bin:/usr/bin", "SANDBOX_CONFIG_PATH=/app/codeops-runtime.toml", "OPENSANDBOX_SERVER_API_KEY=fixture-osb", "CODEOPS_EXECD_ACCESS_TOKEN=fixture-execd"}, "Cmd": []string{"python", "-m", "uvicorn", "opensandbox_server.main:app", "--host", "0.0.0.0", "--port", "8080", "--no-access-log"}, "Labels": map[string]string{"com.docker.compose.project": "localcode", "com.docker.compose.service": "opensandbox-go", "codeops.opensandbox.source_sha": source, "codeops.opensandbox.policy_sha256": policy, "codeops.opensandbox.execd_image": execdImage, "codeops.opensandbox.egress_image": egressImage}}, "State": map[string]bool{"Running": true}, "HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "fixture-network", "Memory": 512 << 20, "NanoCpus": 2e9, "PidsLimit": 128, "Tmpfs": map[string]string{"/run/codeops-control": "rw,noexec,nosuid,nodev,size=64m", "/tmp": "rw,noexec,nosuid,nodev,size=64m"}}, "Mounts": []map[string]any{{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock", "RW": true}, {"Type": "bind", "Source": hostPath(storage), "Destination": hostPath(storage), "RW": false}}}
	dataPath := filepath.Join(t.TempDir(), "objects.json")
	writeObjects := func() {
		data, _ := json.Marshal(objects)
		if err := os.WriteFile(dataPath, data, 0600); err != nil {
			t.Error(err)
		}
	}
	writeObjects()
	productionDockerFixture(t, dataPath)
	t.Setenv("CODE_AGENT_OPENSANDBOX_API_KEY", "fixture-osb")
	t.Setenv("CODE_AGENT_OPENSANDBOX_EXECD_TOKEN", "fixture-execd")
	operation := ""
	currentWorkspace := ""
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OPEN-SANDBOX-API-KEY") != "fixture-osb" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/sandboxes":
			io.WriteString(w, `{"items":[]}`)
		case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
			var body struct {
				Metadata map[string]string `json:"metadata"`
				Volumes  []struct {
					Host struct {
						Path string `json:"path"`
					} `json:"host"`
					ReadOnly bool `json:"readOnly"`
				} `json:"volumes"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Volumes) != 1 || !body.Volumes[0].ReadOnly || !strings.HasPrefix(body.Volumes[0].Host.Path, hostPath(storage)+"/") {
				t.Error("unapproved workspace mounted")
				w.WriteHeader(409)
				return
			}
			operation = body.Metadata["codeops.operation_id"]
			currentWorkspace = body.Volumes[0].Host.Path
			if strings.HasPrefix(currentWorkspace, "/run/desktop/mnt/host/") {
				relative := strings.TrimPrefix(currentWorkspace, "/run/desktop/mnt/host/")
				currentWorkspace = strings.ToUpper(relative[:1]) + ":" + strings.ReplaceAll(relative[1:], "/", `\`)
			}
			labels := map[string]string{"com.docker.compose.project": "localcode", "com.docker.compose.service": "opensandbox-task", "opensandbox.io/id": sandboxID, "codeops.operation_id": operation, "codeops.opensandbox.policy_sha256": policy}
			task := map[string]any{"Id": taskID, "Config": map[string]any{"Image": taskImage, "Labels": labels}, "State": map[string]bool{"Running": true}, "HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "CapAdd": []string{"SETUID", "SETGID"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "container:" + sidecarID, "Memory": 1 << 30, "NanoCpus": 2e9, "PidsLimit": 128, "Tmpfs": map[string]string{"/run/codeops": "rw,exec,nosuid,nodev,size=512m,mode=1777", "/tmp": "rw,noexec,nosuid,nodev,size=64m"}}, "Mounts": []map[string]any{{"Type": "bind", "Source": body.Volumes[0].Host.Path, "Destination": "/workspace", "RW": false}}}
			objects[taskID], objects["sandbox-"+sandboxID] = task, task
			objects[sidecarID] = map[string]any{"Id": sidecarID, "Config": map[string]any{"Image": egressImage, "Env": []string{"OPENSANDBOX_EGRESS_MODE=dns+nft"}, "Labels": map[string]string{"com.docker.compose.project": "localcode", "com.docker.compose.service": "opensandbox-egress", "opensandbox.io/egress-sidecar-for": sandboxID, "codeops.operation_id": operation, "codeops.opensandbox.policy_sha256": policy}}, "State": map[string]bool{"Running": true}, "HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "CapAdd": []string{"NET_ADMIN"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "fixture-network", "Memory": 256 << 20, "NanoCpus": 1e9, "PidsLimit": 64, "Tmpfs": map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=64m"}}}
			writeObjects()
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"id":%q,"status":{"state":"Running"}}`, sandboxID)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/command"):
			calls.Add(1)
			// Real execd starts the stream before execution. A slow trusted
			// checker must not consume the client's response-header timeout.
			fmt.Fprintln(w, `{"type":"init","text":"command-1"}`)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			} else {
				t.Error("fixture command stream cannot flush its init event")
				return
			}
			text := "public structure validated\n"
			if len(graderOutput) > 0 && strings.HasPrefix(filepath.Base(currentWorkspace), "evaluation-grader-") {
				text = graderOutput[0](currentWorkspace)
			}
			fmt.Fprintf(w, "{\"type\":\"stdout\",\"text\":%q}\n{\"type\":\"execution_complete\"}\n", text)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/command/status/"):
			io.WriteString(w, `{"id":"command-1","running":false,"exit_code":0}`)
		case r.Method == "DELETE":
			if r.URL.Query().Get("codeops_operation_id") != operation || r.URL.Query().Get("codeops_container_id") != taskID || r.URL.Query().Get("codeops_sidecar_id") != sidecarID {
				t.Error("deletion ignored physical ownership")
				w.WriteHeader(409)
				return
			}
			delete(objects, taskID)
			delete(objects, "sandbox-"+sandboxID)
			delete(objects, sidecarID)
			writeObjects()
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(endpoint.Close)
	manifest, _ := json.Marshal(map[string]string{"source_sha": source, "endpoint": endpoint.URL + "/v1", "server_container_id": serverID, "server_image": serverImage, "execd_image": execdImage, "egress_image": egressImage, "policy_sha256": policy})
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(manifest)
	return config.SandboxConfig{Enabled: true, Backend: sandbox.BackendOpenSandbox, TrustRoot: storage, Image: taskImage, Toolchain: "go", MemoryLimit: "1g", CPULimit: "2", PidsLimit: 128, TmpfsSize: "64m", OpenSandboxManifest: manifestPath, OpenSandboxManifestSHA256: hex.EncodeToString(hash[:])}
}

var productionDockerBinary = sync.OnceValues(func() (binaryBytes []byte, err error) {
	dir, err := os.MkdirTemp("", "codeops-production-docker-fixture-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			binaryBytes = nil
			err = errors.Join(err, fmt.Errorf("docker fixture cleanup: %w", cleanupErr))
		}
	}()
	source := `package main
import("os";"encoding/json";"fmt")
func main(){a:=os.Args[1:];if len(a)>0&&a[0]=="version"{fmt.Print("28.0.0");return};if len(a)<2{os.Exit(77)};b,e:=os.ReadFile(os.Getenv("CODEOPS_PRODUCTION_DOCKER_OBJECTS"));if e!=nil{os.Exit(78)};var objects map[string]json.RawMessage;if json.Unmarshal(b,&objects)!=nil{os.Exit(79)};v,ok:=objects[a[len(a)-1]];if !ok{fmt.Fprintln(os.Stderr,"error: no such object");os.Exit(1)};fmt.Print(string(v))}`
	path := filepath.Join(dir, "docker.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		return nil, err
	}
	binary := filepath.Join(dir, "docker.exe")
	if body, err := exec.Command("go", "build", "-o", binary, path).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("docker fixture build: %w %s", err, body)
	}
	return os.ReadFile(binary)
})

func productionDockerFixture(t *testing.T, objects string) {
	t.Helper()
	dir := t.TempDir()
	body, err := productionDockerBinary()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "docker.exe")
	if err := os.WriteFile(binary, body, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEOPS_PRODUCTION_DOCKER_OBJECTS", objects)
}

func TestProductionDockerFixtureCopiesStayIndependent(t *testing.T) {
	install := func(label string) string {
		t.Helper()
		objects := filepath.Join(t.TempDir(), "objects.json")
		if err := os.WriteFile(objects, []byte(`{"probe":{"label":"`+label+`"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		productionDockerFixture(t, objects)
		binary, err := exec.LookPath("docker.exe")
		if err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "image", "inspect", "probe").CombinedOutput(); err != nil || string(output) != `{"label":"`+label+`"}` {
			t.Fatalf("independent fixture data: %s %v", output, err)
		}
		return binary
	}
	first, second := install("first"), install("second")
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil || os.SameFile(firstInfo, secondInfo) {
		t.Fatalf("fixture copies do not have independent file identities: %v", err)
	}
	original, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("owned fixture copy mutation"), 0700); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(second); err != nil || !bytes.Equal(original, current) {
		t.Fatalf("overwriting one fixture copy changed its sibling: %v", err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(second); err != nil || !bytes.Equal(original, current) {
		t.Fatalf("deleting one fixture copy changed its sibling: %v", err)
	}
	third := install("third")
	if current, err := os.ReadFile(third); err != nil || !bytes.Equal(original, current) {
		t.Fatalf("copy mutation or deletion changed the cached template: %v", err)
	}
}
