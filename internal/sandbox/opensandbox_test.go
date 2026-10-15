package sandbox_test

import (
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
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/cli"
	appconfig "code-agent/internal/config"
	"code-agent/internal/hooks"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/internal/tools"
	"google.golang.org/grpc"
)

func TestOpenSandboxRunnerDoesNotRunWithoutApprovedRuntime(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "baseline.txt")
	if err := os.WriteFile(marker, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var runner sandbox.Runner = sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: root})
	result, err := runner.Run(context.Background(), sandbox.Request{
		Workspace: root, WorkingDir: root, Program: "go", Args: []string{"test", "./...", "-count=1"},
	})
	if !errors.Is(err, sandbox.ErrSandboxUnavailable) || result.ExitCode != 126 {
		t.Fatalf("unapproved runtime admitted execution: result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "original" {
		t.Fatal("unapproved execution changed the workspace")
	}
}

func TestRoutingRunnerKeepsExplicitOpenSandboxSelectionAndFailsClosed(t *testing.T) {
	root := t.TempDir()
	config := sandbox.Config{Backend: sandbox.Backend("opensandbox"), TrustRoot: root}
	runner, err := sandbox.SelectRunner(config, runtime.GOOS, sandbox.Availability{Docker: true, WSL2: true})
	if err != nil {
		t.Fatalf("explicit selected runtime was rejected before its own admission: %v", err)
	}
	if runner.Backend() != sandbox.Backend("opensandbox") {
		t.Fatal("selected runtime silently changed")
	}
	result, err := runner.Run(context.Background(), sandbox.Request{Workspace: root, WorkingDir: root, Program: "go", Args: []string{"test", "./...", "-count=1"}})
	if !errors.Is(err, sandbox.ErrSandboxUnavailable) || result.ExitCode != 126 {
		t.Fatalf("unapproved runtime did not fail closed: result=%+v err=%v", result, err)
	}
	if _, err := sandbox.SelectRunner(config, "windows", sandbox.Availability{WSL2: true}); err == nil {
		t.Fatal("explicit runtime silently fell back to WSL")
	}
}

func TestOpenSandboxRunnerReturnsCompleteCommandOutcomeAndStopsOwnedSandbox(t *testing.T) {
	for _, scenario := range []string{"changed frozen input", "Python changed frozen input", "Python frozen arbitrary command", "Python frozen missing dependency", "Python frozen wrong toolchain", "complete failed baseline", "tool partial outcome", "tool rejects foreign dependency", "approved Python prerequisites", "Python missing dependency", "cancellation after creation", "Go profile dependency", "complete failed baseline local image IDs", "complete failed baseline registry named sha256", "mismatched local image ID", "mismatched local image ID task", "short local image ID", "terminal keepalive", "missing execd image", "writable server root", "server working directory", "server environment override", "server duplicate environment", "server ungrouped", "server legacy grouping image", "ungrouped task", "foreign group egress", "privileged egress", "writable root", "stricter scratch limit", "unsupported managed volume", "oversized event", "incomplete outcome", "missing running status", "replaced missing container", "replaced logical binding", "unconfirmed termination"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			serverID, containerID, sidecarID := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
			serverImage := "opensandbox-server@sha256:" + strings.Repeat("1", 64)
			execdImage := "opensandbox-execd@sha256:" + strings.Repeat("2", 64)
			egressImage := "opensandbox-egress@sha256:" + strings.Repeat("3", 64)
			taskImage := "golang@sha256:" + strings.Repeat("4", 64)
			if strings.Contains(scenario, "local image ID") {
				serverImage = "sha256:" + strings.Repeat("1", 64)
				execdImage = "sha256:" + strings.Repeat("2", 64)
				egressImage = "sha256:" + strings.Repeat("3", 64)
				taskImage = "sha256:" + strings.Repeat("4", 64)
			}
			if scenario == "short local image ID" {
				execdImage = "sha256:" + strings.Repeat("2", 12)
			}
			if scenario == "complete failed baseline registry named sha256" {
				serverImage = "sha256:5000/" + serverImage
				execdImage = "sha256:5000/" + execdImage
				egressImage = "sha256:5000/" + egressImage
				taskImage = "sha256:5000/" + taskImage
			}
			policy := strings.Repeat("5", 64)
			const sandboxID = "a10e5865-8f00-473e-8c20-5495b9f307f1"
			fixtureFile := filepath.Join(t.TempDir(), "containers.json")
			serverRoot := root
			if runtime.GOOS == "windows" {
				serverRoot = "/run/desktop/mnt/host/" + strings.ToLower(root[:1]) + strings.ReplaceAll(root[2:], `\`, "/")
			}
			objects := map[string]any{
				serverID: map[string]any{"Id": serverID, "Config": map[string]any{"Image": serverImage, "Labels": map[string]string{
					"codeops.opensandbox.source_sha": "f89eff24bbd15ae1c37a47769f7667849e679be0", "codeops.opensandbox.policy_sha256": policy,
					"codeops.opensandbox.execd_image": execdImage, "codeops.opensandbox.egress_image": egressImage,
					"com.docker.compose.project": "localcode", "com.docker.compose.service": "opensandbox-go",
				}, "WorkingDir": "/app", "Env": []string{"PATH=/usr/local/bin:/usr/bin", "SANDBOX_CONFIG_PATH=/app/codeops-runtime.toml", "OPENSANDBOX_SERVER_API_KEY=fixture-lifecycle", "CODEOPS_EXECD_ACCESS_TOKEN=fixture-execd"}, "Cmd": []string{"python", "-m", "uvicorn", "opensandbox_server.main:app", "--host", "0.0.0.0", "--port", "8080", "--no-access-log"}}, "State": map[string]any{"Running": true},
					"HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "codeops-network", "Memory": 512 << 20, "NanoCpus": 2000000000, "PidsLimit": 128, "Tmpfs": map[string]string{"/run/codeops-control": "rw,noexec,nosuid,nodev,size=64m", "/tmp": "rw,noexec,nosuid,nodev,size=64m"}},
					"Mounts":     []map[string]any{{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock", "RW": true}, {"Type": "bind", "Source": serverRoot, "Destination": serverRoot, "RW": false}}},
				sidecarID: map[string]any{"Id": sidecarID, "Config": map[string]any{"Image": egressImage, "Env": []string{"OPENSANDBOX_EGRESS_MODE=dns+nft"}, "Labels": map[string]string{"opensandbox.io/egress-sidecar-for": sandboxID, "codeops.operation_id": strings.Repeat("7", 64), "codeops.opensandbox.policy_sha256": policy}}, "State": map[string]any{"Running": true},
					"HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "CapAdd": []string{"NET_ADMIN"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "codeops-network", "Memory": 256 << 20, "NanoCpus": 1000000000, "PidsLimit": 64, "Tmpfs": map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=64m"}}},
				taskImage:   map[string]any{"RepoDigests": []string{taskImage}},
				serverImage: map[string]any{"RepoDigests": []string{serverImage}, "Config": map[string]any{"Labels": map[string]string{"codeops.docker.project": "localcode"}, "Env": []string{"PATH=/usr/local/bin:/usr/bin", "SANDBOX_CONFIG_PATH=/app/codeops-runtime.toml"}}},
				execdImage:  map[string]any{"RepoDigests": []string{execdImage}},
				egressImage: map[string]any{"RepoDigests": []string{egressImage}},
			}
			if scenario == "writable server root" {
				objects[serverID].(map[string]any)["HostConfig"].(map[string]any)["ReadonlyRootfs"] = false
			}
			if scenario == "approved Python prerequisites" || strings.HasPrefix(scenario, "Python ") {
				objects[serverID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["codeops.opensandbox.toolchain"] = "python"
				objects[serverID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["com.docker.compose.service"] = "opensandbox-python"
			}
			objects[sidecarID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["com.docker.compose.project"] = "localcode"
			objects[sidecarID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["com.docker.compose.service"] = "opensandbox-egress"
			if scenario == "foreign group egress" {
				objects[sidecarID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["com.docker.compose.project"] = "other-project"
			}
			if scenario == "server ungrouped" {
				delete(objects[serverID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string), "com.docker.compose.project")
			}
			if scenario == "server legacy grouping image" {
				delete(objects[serverImage].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string), "codeops.docker.project")
			}
			if scenario == "server working directory" {
				objects[serverID].(map[string]any)["Config"].(map[string]any)["WorkingDir"] = serverRoot
			}
			if scenario == "server environment override" || scenario == "server duplicate environment" {
				config := objects[serverID].(map[string]any)["Config"].(map[string]any)
				entry := "PYTHONPATH=" + serverRoot
				if scenario == "server duplicate environment" {
					entry = "SANDBOX_CONFIG_PATH=/workspace/unapproved.toml"
				}
				config["Env"] = append(config["Env"].([]string), entry)
			}
			if scenario == "privileged egress" {
				objects[sidecarID].(map[string]any)["HostConfig"].(map[string]any)["CapAdd"] = []string{"NET_ADMIN", "SYS_ADMIN"}
			}
			if strings.Contains(scenario, "local image ID") {
				for _, image := range []string{serverImage, execdImage, egressImage, taskImage} {
					objects[image].(map[string]any)["Id"] = image
					objects[image].(map[string]any)["RepoDigests"] = []string{}
				}
			}
			if strings.HasPrefix(scenario, "mismatched local image ID") {
				image := execdImage
				if strings.HasSuffix(scenario, " task") {
					image = taskImage
				}
				objects[image] = map[string]any{"Id": "sha256:" + strings.Repeat("9", 64), "RepoDigests": []string{image}}
			}
			if scenario == "missing execd image" {
				delete(objects, execdImage)
			}
			writeObjects := func() error {
				data, err := json.Marshal(objects)
				if err != nil {
					return err
				}
				return os.WriteFile(fixtureFile, data, 0600)
			}
			if err := writeObjects(); err != nil {
				t.Fatal(err)
			}
			openSandboxDockerFixture(t, fixtureFile)
			t.Setenv("CODE_AGENT_OPENSANDBOX_API_KEY", "fixture-lifecycle")
			t.Setenv("CODE_AGENT_OPENSANDBOX_EXECD_TOKEN", "fixture-execd")
			created, executed, deleted := 0, 0, 0
			operation := strings.Repeat("7", 64)
			var ledgerTasks *session.TaskWorkspaces
			var cliLedger *session.Manager
			postHookUnknown := false
			ledgerSessionID := ""
			sidecarFixture := objects[sidecarID]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"items":[]}`)
					return
				}
				if r.Header.Get("OPEN-SANDBOX-API-KEY") != "fixture-lifecycle" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch {
				case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
					var body struct {
						Metadata map[string]string `json:"metadata"`
						Env      map[string]string `json:"env"`
						Volumes  []struct {
							MountPath string `json:"mountPath"`
							ReadOnly  bool   `json:"readOnly"`
							Host      struct {
								Path string `json:"path"`
							} `json:"host"`
						} `json:"volumes"`
						NetworkPolicy struct {
							DefaultAction string `json:"defaultAction"`
							Egress        []struct {
								Action string `json:"action"`
								Target string `json:"target"`
							} `json:"egress"`
						} `json:"networkPolicy"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Volumes) != 1 || !body.Volumes[0].ReadOnly || body.Volumes[0].MountPath != "/workspace" || body.NetworkPolicy.DefaultAction != "deny" {
						t.Error("creation omitted the read-only workspace or offline policy")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if len(body.NetworkPolicy.Egress) != 2 || body.NetworkPolicy.Egress[0].Action != "deny" || body.NetworkPolicy.Egress[0].Target != "0.0.0.0/0" || body.NetworkPolicy.Egress[1].Action != "deny" || body.NetworkPolicy.Egress[1].Target != "::/0" {
						t.Error("offline policy omitted explicit IPv4/IPv6 denial before upstream nameserver allowance")
					}
					if _, reserved := body.Env["OPENSANDBOX_EGRESS_MODE"]; reserved {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					labels := body.Metadata
					if len(labels) != 2 {
						t.Error("client added metadata outside the strict runtime contract")
					}
					if cliLedger != nil {
						if _, ok, err := cliLedger.ResumeLatest(r.Context()); err != nil || !ok {
							t.Error("CLI intent absent before Hook creation")
							w.WriteHeader(http.StatusConflict)
							return
						}
						invocation := cliLedger.Current().Invocations[0]
						if invocation.Status != session.InvocationDispatched {
							t.Error("Hook preceded its durable CLI dispatch")
							w.WriteHeader(http.StatusConflict)
							return
						}
						operation = invocation.OperationID
						objects[sidecarID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["codeops.operation_id"] = operation
					}
					if ledgerTasks != nil {
						view, err := ledgerTasks.Inspect(r.Context(), 7, ledgerSessionID)
						if err != nil || view.Test == nil || view.Test.State != "unknown" {
							t.Error("runtime effect preceded its durable Ledger intent")
							w.WriteHeader(http.StatusConflict)
							return
						}
						operation = view.Test.OperationID
						objects[sidecarID].(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["codeops.operation_id"] = operation
					}
					if labels["codeops.operation_id"] != operation {
						t.Error("runtime effect did not use the caller's persisted operation identity")
					}
					labels["opensandbox.io/id"] = sandboxID
					labels["com.docker.compose.project"] = "localcode"
					labels["com.docker.compose.service"] = "opensandbox-task"
					if scenario == "ungrouped task" {
						delete(labels, "com.docker.compose.project")
					}
					objects["sandbox-"+sandboxID] = map[string]any{"Id": containerID, "Config": map[string]any{"Image": taskImage, "Labels": labels}, "State": map[string]any{"Running": true},
						"HostConfig": map[string]any{"ReadonlyRootfs": true, "Privileged": false, "CapDrop": []string{"ALL"}, "CapAdd": []string{"SETUID", "SETGID"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "container:" + sidecarID, "Memory": 1 << 30, "NanoCpus": 2000000000, "PidsLimit": 128, "Tmpfs": map[string]string{"/run/codeops": "rw,exec,nosuid,nodev,size=512m,mode=1777", "/tmp": "rw,noexec,nosuid,nodev,size=64m"}},
						"Mounts":     []map[string]any{{"Type": "bind", "Source": body.Volumes[0].Host.Path, "Destination": "/workspace", "RW": false}}}
					objects[containerID] = objects["sandbox-"+sandboxID]
					if scenario == "unsupported managed volume" {
						labels["opensandbox.io/volume-managed-by"] = `["opensandbox-runtime-` + sandboxID + `"]`
						objects[containerID].(map[string]any)["Mounts"] = append(objects[containerID].(map[string]any)["Mounts"].([]map[string]any), map[string]any{"Type": "volume", "Name": "opensandbox-runtime-" + sandboxID, "Destination": "/opt/opensandbox", "RW": true})
					}
					if scenario == "writable root" {
						objects[containerID].(map[string]any)["HostConfig"].(map[string]any)["ReadonlyRootfs"] = false
					}
					if err := writeObjects(); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					created++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprintf(w, `{"id":%q,"status":{"state":"Running"}}`, sandboxID)
				case r.Method == "POST" && r.URL.Path == "/v1/sandboxes/"+sandboxID+"/proxy/44772/command":
					var body struct {
						Argv    []string `json:"argv"`
						Command string   `json:"command"`
						Cwd     string   `json:"cwd"`
						UID     int      `json:"uid"`
						GID     int      `json:"gid"`
					}
					wantArgv := []string{"go", "test", "./...", "-count=1"}
					if scenario == "tool partial outcome" {
						wantArgv = []string{"sh", "-c", "go test ./... -count=1"}
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || !reflect.DeepEqual(body.Argv, wantArgv) || body.Command != "" || body.Cwd != "/workspace" || body.UID != 65534 || body.GID != 65534 || r.Header.Get("X-EXECD-ACCESS-TOKEN") != "fixture-execd" {
						t.Error("command or execution identity changed across the API")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					executed++
					w.Header().Set("Content-Type", "text/event-stream")
					if scenario == "oversized event" {
						fmt.Fprintf(w, "{\"type\":\"init\",\"text\":\"command-1\"}\n{\"type\":\"stdout\",\"text\":%q}\n", strings.Repeat("x", 70000))
						return
					}
					if scenario == "incomplete outcome" || scenario == "tool partial outcome" || postHookUnknown {
						fmt.Fprint(w, "{\"type\":\"init\",\"text\":\"command-1\"}\n{\"type\":\"stdout\",\"text\":\"partial failure\\n\"}\n")
						return
					}
					fmt.Fprint(w, "{\"type\":\"init\",\"text\":\"command-1\"}\n\n{\"type\":\"stdout\",\"text\":\"FAIL public baseline\\n\"}\n\n{\"type\":\"error\",\"error\":{\"ename\":\"CommandExecError\"}}\n\n")
					if scenario == "terminal keepalive" {
						fmt.Fprint(w, "{\"type\":\"ping\"}\n\n")
					}
				case r.Method == "GET" && r.URL.Path == "/v1/sandboxes/"+sandboxID+"/proxy/44772/command/status/command-1":
					if strings.HasPrefix(scenario, "replaced ") {
						if scenario == "replaced missing container" {
							delete(objects, containerID)
						}
						replacementID := strings.Repeat("d", 64)
						objects["sandbox-"+sandboxID] = map[string]any{"Id": replacementID, "Config": map[string]any{"Labels": map[string]string{"opensandbox.io/id": sandboxID, "codeops.operation_id": strings.Repeat("8", 64)}}}
						objects[replacementID] = objects["sandbox-"+sandboxID]
						if err := writeObjects(); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
					}
					if scenario == "missing running status" {
						fmt.Fprint(w, `{"id":"command-1","exit_code":0}`)
						return
					}
					fmt.Fprint(w, `{"id":"command-1","running":false,"exit_code":7}`)
				case r.Method == "DELETE" && r.URL.Path == "/v1/sandboxes/"+sandboxID:
					query := r.URL.Query()
					if query.Get("codeops_container_id") != containerID || query.Get("codeops_sidecar_id") != sidecarID || query.Get("codeops_operation_id") != operation {
						w.WriteHeader(http.StatusConflict)
						return
					}
					delete(objects, "sandbox-"+sandboxID)
					delete(objects, containerID)
					delete(objects, sidecarID)
					if scenario == "unconfirmed termination" {
						objects["__daemon_unavailable__"] = true
					}
					if err := writeObjects(); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					deleted++
					w.WriteHeader(http.StatusNoContent)
				case r.Method == "GET" && r.URL.Path == "/v1/sandboxes/"+sandboxID:
					w.WriteHeader(http.StatusNotFound)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			manifest := []byte(fmt.Sprintf(`{"source_sha":"f89eff24bbd15ae1c37a47769f7667849e679be0","endpoint":%q,"server_container_id":%q,"server_image":%q,"execd_image":%q,"egress_image":%q,"policy_sha256":%q}`, server.URL+"/v1", serverID, serverImage, execdImage, egressImage, policy))
			manifestPath := filepath.Join(t.TempDir(), "runtime.json")
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(manifest)
			config := sandbox.Config{TrustRoot: root, Toolchain: "go", Image: taskImage, OpenSandboxManifest: manifestPath, OpenSandboxManifestSHA256: hex.EncodeToString(digest[:])}
			if strings.HasPrefix(scenario, "Python ") {
				config.Toolchain = "python"
			}
			if scenario == "approved Python prerequisites" {
				config.Toolchain = "python"
				if err := sandbox.NewOpenSandboxRunner(config).Ready(context.Background()); err != nil {
					t.Fatalf("approved Python runtime was not ready: %v", err)
				}
				result, err := sandbox.NewOpenSandboxRunner(config).Run(context.Background(), sandbox.Request{Workspace: root, WorkingDir: root, OperationID: operation, Program: "git", Args: []string{"status"}})
				if !errors.Is(err, sandbox.ErrSandboxUnavailable) || !strings.Contains(err.Error(), "Git") || result.RuntimeID != "" || created != 0 || executed != 0 {
					t.Fatalf("unsupported Python Git was dispatched or disguised as dependency failure: %+v %v", result, err)
				}
				return
			}
			if scenario == "stricter scratch limit" {
				config.TmpfsSize = "16m"
			}
			if scenario == "changed frozen input" || scenario == "Python changed frozen input" || strings.HasPrefix(scenario, "Python frozen ") {
				if runtime.GOOS != "windows" {
					t.Skip("requires the Windows native frozen-input pin contract")
				}
				const source = "package calc\n"
				name := filepath.Join(root, "calc.go")
				if err := os.WriteFile(name, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				sourceDigest := sha256.Sum256([]byte(source))
				input, err := sandbox.FreezeInput(context.Background(), root, root, operation, []sandbox.InputFile{{Path: "calc.go", Exists: true, Size: int64(len(source)), Mode: uint32(info.Mode().Perm()), SHA256: hex.EncodeToString(sourceDigest[:])}})
				if err != nil {
					t.Fatal(err)
				}
				runner := sandbox.NewOpenSandboxRunner(config)
				if err := runner.Ready(context.Background()); err != nil {
					t.Fatal("approved fixture runtime is not ready")
				}
				if strings.Contains(scenario, "changed frozen input") {
					if err := os.WriteFile(input.Path, []byte("untrusted replacement archive"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				request := sandbox.Request{Workspace: input.Path, FrozenInput: input, OperationID: operation, Program: "go", Args: []string{"test", "./...", "-count=1"}}
				reason := "frozen input"
				if strings.HasPrefix(scenario, "Python ") {
					request.Program, request.Args, request.DependencyOperationID = "python", []string{"-c", sandbox.PythonUnittestScript}, strings.Repeat("8", 64)
					reason = "frozen input archive checksum differs"
				}
				switch scenario {
				case "Python frozen arbitrary command":
					request.Args, reason = []string{"-c", "print('unapproved')"}, "outside its test contract"
				case "Python frozen missing dependency":
					request.DependencyOperationID, reason = "", "outside its test contract"
				case "Python frozen wrong toolchain":
					request.Program, reason = "go", "matching approved toolchain"
				}
				result, err := runner.Run(context.Background(), request)
				if !errors.Is(err, sandbox.ErrSandboxUnavailable) || !strings.Contains(err.Error(), reason) || result.InputSHA256 != "" || result.RuntimeID != "" || created != 0 || executed != 0 || deleted != 0 {
					t.Fatalf("changed frozen archive was executed or bound to a test: %+v lifecycle=%d/%d/%d %v", result, created, executed, deleted, err)
				}
				return
			}
			if scenario == "tool rejects foreign dependency" {
				executor := tools.NewExecutor(root)
				defer executor.Close()
				executor.SetSandbox(sandbox.NewOpenSandboxRunner(config))
				result, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Bash", OperationID: operation, DependencyOperationID: strings.Repeat("8", 64), Arguments: map[string]any{"command": "echo bypassed > escaped.txt"}})
				if !errors.Is(err, sandbox.ErrSandboxUnavailable) || result.ExitCode != 126 || created != 0 || executed != 0 || deleted != 0 {
					t.Fatalf("tool lost the approved dependency boundary: %+v lifecycle=%d/%d/%d %v", result, created, executed, deleted, err)
				}
				if _, err := os.Stat(filepath.Join(root, "escaped.txt")); !os.IsNotExist(err) {
					t.Fatal("dependency refusal fell back to the host")
				}
				return
			}
			if scenario == "tool partial outcome" {
				executor := tools.NewExecutor(root)
				defer executor.Close()
				executor.SetSandbox(sandbox.NewOpenSandboxRunner(config))
				result, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Bash", OperationID: operation, Arguments: map[string]any{"command": "go test ./... -count=1"}})
				if !errors.Is(err, sandbox.ErrSandboxOutcomeUnknown) || !result.OutcomeUnknown || !result.Stopped || !result.Truncated || !strings.HasPrefix(result.Output, "partial failure\n") || !strings.Contains(result.RuntimeID, containerID) || created != 1 || executed != 1 || deleted != 1 {
					t.Fatalf("tool discarded remote identity or incomplete lifecycle: %+v lifecycle=%d/%d/%d %v", result, created, executed, deleted, err)
				}
				objects[sidecarID] = sidecarFixture
				if err := writeObjects(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
				t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				transport := grpc.NewServer()
				codeagentpb.RegisterOrchestratorServer(transport, failedReadTransport{Name: "Bash", Parameters: `{"command":"go test ./... -count=1"}`})
				go transport.Serve(listener)
				defer transport.Stop()
				cfg := appconfig.Default(root)
				cfg.WorkingDir = root
				cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = listener.Addr().String(), false, false
				cfg.Sandbox = appconfig.SandboxConfig{Backend: sandbox.BackendOpenSandbox, Enabled: true, TrustRoot: root, Toolchain: "go", Image: taskImage, OpenSandboxManifest: manifestPath, OpenSandboxManifestSHA256: hex.EncodeToString(digest[:])}
				cfg.Hooks = []appconfig.HookConfig{{Type: "post_tool", Matcher: "Bash", Program: "sh", Args: []string{"-c", "go test ./... -count=1"}}}
				cliLedger = session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
				defer cliLedger.Close()
				app := cli.NewApp(cfg, strings.NewReader("run baseline\ny\ny\n"), io.Discard, io.Discard)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := app.Run(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := cliLedger.Resume(context.Background(), cliLedger.Current().ID); err != nil {
					t.Fatal(err)
				}
				retained := cliLedger.Current().Invocations[0]
				if retained.Status != session.InvocationUnknown || !retained.OutcomeUnknown || len(retained.Runtimes) != 1 || retained.Runtimes[0].Phase != "tool" || !retained.Stopped || !retained.Truncated || !strings.Contains(retained.Output, "partial failure") || created != 2 || executed != 2 || deleted != 2 {
					t.Fatalf("unknown tool dispatched a post Hook or replayed its effect: %+v lifecycle=%d/%d/%d", retained, created, executed, deleted)
				}
				return
			}
			request := sandbox.Request{Workspace: root, WorkingDir: root, OperationID: strings.Repeat("7", 64), Program: "go", Args: []string{"test", "./...", "-count=1"}}
			if scenario == "Go profile dependency" {
				request.DependencyOperationID = strings.Repeat("8", 64)
			}
			runCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancellation after creation" {
				marker := filepath.Join(t.TempDir(), "ownership-lookup")
				t.Setenv("CODEOPS_CANCEL_LOOKUP", marker)
				go func() {
					for attempt := 0; attempt < 200; attempt++ {
						if _, err := os.Stat(marker); err == nil {
							cancel()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
				}()
			}
			result, err := sandbox.NewOpenSandboxRunner(config).Run(runCtx, request)
			if scenario == "cancellation after creation" {
				if !errors.Is(err, sandbox.ErrSandboxOutcomeUnknown) || !result.Stopped || result.ExitCode != 126 || created != 1 || executed != 0 || deleted != 1 || !strings.Contains(result.RuntimeID, containerID) {
					t.Fatalf("cancellation lost exact ownership or cleanup: %+v lifecycle=%d/%d/%d %v", result, created, executed, deleted, err)
				}
				return
			}
			if scenario == "incomplete outcome" || scenario == "unconfirmed termination" {
				encoded, _ := json.Marshal(result)
				var lifecycle struct{ Stopped bool }
				if json.Unmarshal(encoded, &lifecycle) != nil || lifecycle.Stopped != (scenario == "incomplete outcome") {
					t.Fatalf("confirmed resource stop was confused with execution outcome: %s", encoded)
				}
			}
			if strings.HasPrefix(scenario, "Python ") || scenario == "Go profile dependency" || scenario == "missing execd image" || strings.HasPrefix(scenario, "mismatched local image ID") || scenario == "short local image ID" || scenario == "writable server root" || strings.HasPrefix(scenario, "server ") {
				if !errors.Is(err, sandbox.ErrSandboxUnavailable) || result.ExitCode != 126 || created != 0 || executed != 0 || deleted != 0 {
					t.Fatalf("missing runtime component admitted a sandbox: result=%+v lifecycle=%d/%d/%d err=%v", result, created, executed, deleted, err)
				}
				return
			}
			if scenario != "complete failed baseline" && scenario != "complete failed baseline local image IDs" && scenario != "complete failed baseline registry named sha256" && scenario != "terminal keepalive" {
				wantExecuted, wantError := 1, sandbox.ErrSandboxOutcomeUnknown
				wantDeleted := 1
				if strings.HasPrefix(scenario, "replaced ") || scenario == "unsupported managed volume" {
					wantDeleted = 0
				}
				if scenario == "unsupported managed volume" {
					wantExecuted = 0
				}
				if scenario == "writable root" || scenario == "stricter scratch limit" || scenario == "privileged egress" || scenario == "ungrouped task" || scenario == "foreign group egress" {
					wantExecuted, wantError = 0, sandbox.ErrSandboxUnavailable
				}
				if !errors.Is(err, wantError) || result.ExitCode != 126 || created != 1 || executed != wantExecuted || deleted != wantDeleted || !strings.Contains(result.RuntimeID, containerID) {
					t.Fatalf("unsafe or unknown outcome admitted: result=%+v lifecycle=%d/%d/%d err=%v", result, created, executed, deleted, err)
				}
				if scenario == "incomplete outcome" && result.Output != "partial failure\n" {
					t.Fatal("partial failure output was discarded")
				}
				if scenario == "incomplete outcome" && runtime.GOOS == "windows" {
					repo := t.TempDir()
					for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
						if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
							t.Fatalf("Git fixture: %s %v", output, err)
						}
					}
					if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/stopped\n\ngo 1.20\n"), 0600); err != nil {
						t.Fatal(err)
					}
					for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
						if err := exec.Command("git", append([]string{"-C", repo}, args...)...).Run(); err != nil {
							t.Fatal(err)
						}
					}
					ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
					if err != nil {
						t.Fatal(err)
					}
					defer ledger.Close()
					workbench := session.NewWorkbench(ledger, nil)
					createdSession, err := workbench.CreateWithWorkingDir(context.Background(), 7, "repo", "stopped unknown", "", repo)
					if err != nil {
						t.Fatal(err)
					}
					ledgerSessionID = createdSession.ID
					owner := func(context.Context) (uint, error) { return 7, nil }
					ledgerTasks = session.NewTaskWorkspaces(workbench, owner, repo, root)
					if view, err := ledgerTasks.Prepare(context.Background(), 7, ledgerSessionID, 1, "prepare"); err != nil || view.State != "prepared" {
						t.Fatalf("prepare: %+v %v", view, err)
					}
					budget := admission.NewBudget(ledger)
					modelRuntime := sandbox.NewOpenSandboxRunner(config)
					profile := session.TaskBaselineRuntime{Runner: modelRuntime, Ready: modelRuntime.Ready, Acquire: func(ctx context.Context, owner uint, id string) error {
						_, err := budget.AcquireTask(ctx, owner, id)
						return err
					}, Finish: func(ctx context.Context, owner uint, id string) error {
						_, err := budget.FinishTask(ctx, owner, id)
						return err
					}}
					ledgerTasks.WithBaselineRuntime(profile)
					modelWorkspace, err := ledgerTasks.ModelWorkspace(context.Background(), 7, ledgerSessionID)
					if err != nil || modelWorkspace.WorkingDir == repo || !strings.HasPrefix(modelWorkspace.WorkingDir, root+string(os.PathSeparator)) || modelWorkspace.DependencyOperationID != "" {
						t.Fatalf("admitted model did not receive the isolated copy: %+v %v", modelWorkspace, err)
					}
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					captured := make(chan *codeagentpb.UserInput, 1)
					transport := grpc.NewServer()
					codeagentpb.RegisterOrchestratorServer(transport, workspaceOrchestrator{captured: captured})
					go transport.Serve(listener)
					defer transport.Stop()
					client, err := orchestrator.NewClient(listener.Addr().String())
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					actor, err := identity.Default().BindSession(ledgerSessionID)
					if err != nil {
						t.Fatal(err)
					}
					runner := session.NewSessionRunner(workbench, client, nil, session.SessionRunnerOptions{Workspace: ledgerTasks.ModelWorkspace})
					run, err := runner.SubmitMessage(context.Background(), session.SubmitMessageCommand{RequestID: "model-workspace", SessionID: ledgerSessionID, OwnerID: 7, ExpectedSeq: 3, Content: "inspect the task copy", Actor: actor})
					if err != nil {
						t.Fatal(err)
					}
					select {
					case input := <-captured:
						if input.WorkingDir != modelWorkspace.WorkingDir || input.ContextEnvelope == nil || !input.HarnessManaged {
							t.Fatal("Runner forwarded the original repository or omitted its admitted Context")
						}
					case <-time.After(5 * time.Second):
						t.Fatal("admitted workspace did not reach the external transport")
					}
					for attempt := 0; attempt < 500; attempt++ {
						view, err := workbench.Get(context.Background(), 7, ledgerSessionID)
						if err != nil {
							t.Fatal(err)
						}
						if view.Run != nil && view.Run.RunID == run.RunID && view.Run.Status == session.RunCompleted {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					if err := runner.Close(); err != nil {
						t.Fatal(err)
					}
					current, err := workbench.Get(context.Background(), 7, ledgerSessionID)
					if err != nil || current.Run == nil || current.Run.Status != session.RunCompleted {
						t.Fatalf("model workspace run failed: %+v %v", current.Run, err)
					}
					baselineSeq := int64(current.EventCount)
					objects[sidecarID] = sidecarFixture
					if err := writeObjects(); err != nil {
						t.Fatal(err)
					}
					view, err := ledgerTasks.RunBaseline(context.Background(), 7, ledgerSessionID, baselineSeq, "baseline")
					if err != nil || view.Test == nil || view.Test.State != "unknown" || view.Test.Output != "partial failure\n" || view.Test.ExitCode != nil || view.EventCount != int(baselineSeq+2) {
						t.Fatalf("stopped execution became success or lost partial output: %+v %v", view, err)
					}
					encoded, _ := json.Marshal(view.Test)
					var stopped struct{ Stopped bool }
					if json.Unmarshal(encoded, &stopped) != nil || !stopped.Stopped {
						t.Fatalf("confirmed stop missing from Ledger projection: %s", encoded)
					}
					batch, err := budget.Inspect(context.Background(), 7)
					if err != nil || batch.TaskID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
						t.Fatalf("confirmed stopped workload retained a slot or reset budget: %+v %v", batch, err)
					}
					reopened := session.NewTaskWorkspaces(session.NewWorkbench(ledger, nil), owner, repo, root).WithBaselineRuntime(profile)
					again, err := reopened.RunBaseline(context.Background(), 7, ledgerSessionID, 3, "baseline")
					againJSON, _ := json.Marshal(again.Test)
					if err != nil || string(againJSON) != string(encoded) || created != 2 || executed != 2 || deleted != 2 {
						t.Fatalf("reopen replayed unknown execution: %+v lifecycle=%d/%d/%d %v", again, created, executed, deleted, err)
					}
					if _, err := reopened.ModelWorkspace(context.Background(), 7, ledgerSessionID); err != nil {
						t.Fatalf("confirmed stopped baseline prevented model review: %v", err)
					}
					if _, err := ledger.Append(context.Background(), ledgerSessionID, int64(again.EventCount), "tool/dispatched", map[string]any{"run_id": "interrupted-run", "tool_call_id": "missing-result", "tool_name": "Bash"}); err != nil {
						t.Fatal(err)
					}
					if _, err := reopened.ModelWorkspace(context.Background(), 7, ledgerSessionID); !errors.Is(err, session.ErrSessionStateConflict) {
						t.Fatal("missing shell result did not block the next model attempt")
					}
				}
				if scenario == "oversized event" && !result.Truncated {
					t.Fatal("oversized event was discarded without recording incomplete output")
				}
				if scenario == "unconfirmed termination" && result.Output != "FAIL public baseline\n" {
					t.Fatal("failure output was discarded after cleanup failure")
				}
				return
			}
			if err != nil || result.ExitCode != 7 || result.Output != "FAIL public baseline\n" || result.Truncated {
				t.Fatalf("complete failed-test outcome was not retained: result=%+v err=%v", result, err)
			}
			if created != 1 || executed != 1 || deleted != 1 {
				t.Fatalf("execution lifecycle = %d/%d/%d, want one owned sandbox", created, executed, deleted)
			}
			if scenario == "complete failed baseline" {
				manager := session.NewManager(session.NewMemoryStore())
				manager.NewSession(root)
				intent, err := manager.BeginInvocation("hook-parent", "Read", `{}`)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := manager.MarkInvocationDispatched(intent.ID); err != nil {
					t.Fatal(err)
				}
				operation = intent.OperationID
				sidecarFixture.(map[string]any)["Config"].(map[string]any)["Labels"].(map[string]string)["codeops.operation_id"] = operation
				objects[sidecarID] = sidecarFixture
				if err := writeObjects(); err != nil {
					t.Fatal(err)
				}
				engine := hooks.NewEngine()
				engine.SetSandbox(sandbox.NewOpenSandboxRunner(config), root)
				engine.RegisterCommandHook(hooks.CommandHook{Phase: hooks.PhasePreTool, Program: "go", Args: []string{"test", "./...", "-count=1"}})
				results, hookErr := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{OperationID: operation, RecordRuntime: func(phase hooks.Phase, result hooks.Result) error {
					id, _ := result.Values["runtime_id"].(string)
					stopped, _ := result.Values["stopped"].(bool)
					return manager.RecordInvocationRuntime(intent.ID, string(phase), id, stopped)
				}})
				if hookErr == nil || len(results) != 1 || results[0].Values["output"] != "FAIL public baseline" || results[0].Values["exit_code"] != 7 || results[0].Values["stopped"] != true || !strings.Contains(fmt.Sprint(results[0].Values["runtime_id"]), containerID) || created != 2 || executed != 2 || deleted != 2 {
					t.Fatalf("Hook lost persisted identity or Runner output/lifecycle: %+v lifecycle=%d/%d/%d %v", results, created, executed, deleted, hookErr)
				}
				record := manager.Current().Invocations[0]
				if record.Status != session.InvocationDispatched || record.Stopped || len(record.Runtimes) != 1 || record.Runtimes[0].Phase != "pre_tool" || !record.Runtimes[0].Stopped {
					t.Fatalf("Hook receipt was not durable before returning to the next effect: %+v", record)
				}
				postHookUnknown = true
				objects[sidecarID] = sidecarFixture
				if err := writeObjects(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
				t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				transport := grpc.NewServer()
				codeagentpb.RegisterOrchestratorServer(transport, failedReadTransport{})
				go transport.Serve(listener)
				defer transport.Stop()
				cfg := appconfig.Default(root)
				cfg.WorkingDir = root
				cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = listener.Addr().String(), false, false
				cfg.Sandbox = appconfig.SandboxConfig{Backend: sandbox.BackendOpenSandbox, Enabled: true, TrustRoot: root, Toolchain: "go", Image: taskImage, OpenSandboxManifest: manifestPath, OpenSandboxManifestSHA256: hex.EncodeToString(digest[:])}
				cfg.Hooks = []appconfig.HookConfig{{Type: "post_tool", Matcher: "Read", Program: "go", Args: []string{"test", "./...", "-count=1"}}}
				cliLedger = session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
				defer cliLedger.Close()
				app := cli.NewApp(cfg, strings.NewReader("inspect missing file\n"), io.Discard, io.Discard)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := app.Run(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := cliLedger.Resume(context.Background(), cliLedger.Current().ID); err != nil {
					t.Fatalf("CLI reopen: %v", err)
				}
				retained := cliLedger.Current().Invocations[0]
				if retained.Status != session.InvocationUnknown || !retained.OutcomeUnknown || !retained.Stopped || !retained.Truncated || !strings.Contains(retained.Output, "partial failure") || len(retained.Runtimes) != 1 || retained.Runtimes[0].Phase != "post_tool" || !strings.Contains(retained.RuntimeID, containerID) || created != 3 || executed != 3 || deleted != 3 {
					t.Fatalf("failed Read hid unknown post-Hook or replayed it: %+v lifecycle=%d/%d/%d", retained, created, executed, deleted)
				}
			}
		})
	}
}

type workspaceOrchestrator struct {
	codeagentpb.UnimplementedOrchestratorServer
	captured chan *codeagentpb.UserInput
}

func (fixture workspaceOrchestrator) Converse(stream grpc.BidiStreamingServer[codeagentpb.HarnessMessage, codeagentpb.OrchestratorMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	fixture.captured <- message.GetUserInput()
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Success: true, Message: "inspected"}}})
}

type failedReadTransport struct {
	codeagentpb.UnimplementedOrchestratorServer
	Name       string
	Parameters string
}

func (failedReadTransport) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok"}, nil
}

func (fixture failedReadTransport) Converse(stream grpc.BidiStreamingServer[codeagentpb.HarnessMessage, codeagentpb.OrchestratorMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	for range 2 {
		name, params := fixture.Name, fixture.Parameters
		if name == "" {
			name, params = "Read", `{"path":"missing-file.txt"}`
		}
		if err := stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_ToolRequest{ToolRequest: &codeagentpb.ToolRequest{ToolCallId: "read-with-post", ToolName: name, ParametersJson: params}}}); err != nil {
			return err
		}
		if _, err := stream.Recv(); err != nil {
			return err
		}
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Message: "expected unknown post-Hook"}}})
}

func openSandboxDockerFixture(t *testing.T, data string) {
	t.Helper()
	dir := t.TempDir()
	source := `package main
import("os";"encoding/json";"fmt";"strings";"time")
func main(){
 a:=os.Args[1:]; if len(a)>0 && a[0]=="version"{fmt.Print("28.0.0");return}; if len(a)<2 || (a[0]!="inspect" && !(a[0]=="image" && a[1]=="inspect")){os.Exit(77)}
 if marker:=os.Getenv("CODEOPS_CANCEL_LOOKUP");marker!=""&&strings.HasPrefix(a[len(a)-1],"sandbox-"){if os.WriteFile(marker,[]byte("lookup"),0600)!=nil{os.Exit(80)};time.Sleep(300*time.Millisecond)}
 b,e:=os.ReadFile(os.Getenv("CODEOPS_TEST_DOCKER_OBJECTS"));if e!=nil{os.Exit(78)}
 var objects map[string]json.RawMessage;if json.Unmarshal(b,&objects)!=nil{os.Exit(79)}
 if _,bad:=objects["__daemon_unavailable__"];bad{fmt.Fprintln(os.Stderr,"Cannot connect to the Docker daemon");os.Exit(1)}
 value,ok:=objects[a[len(a)-1]];if !ok{fmt.Fprintln(os.Stderr,"error: no such object");os.Exit(1)}
 fmt.Print(string(value))
}`
	path := filepath.Join(dir, "docker.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "docker")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, path).CombinedOutput(); err != nil {
		t.Fatalf("external CLI fixture: %s: %v", output, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEOPS_TEST_DOCKER_OBJECTS", data)
}
