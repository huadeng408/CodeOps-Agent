package sandbox_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/sandbox"
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
	for _, scenario := range []string{"complete failed baseline", "complete failed baseline local image IDs", "complete failed baseline registry named sha256", "mismatched local image ID", "mismatched local image ID task", "short local image ID", "terminal keepalive", "missing execd image", "writable server root", "server working directory", "server environment override", "server duplicate environment", "privileged egress", "writable root", "stricter scratch limit", "unsupported managed volume", "oversized event", "incomplete outcome", "missing running status", "replaced missing container", "replaced logical binding", "unconfirmed termination"} {
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
				}, "WorkingDir": "/app", "Env": []string{"PATH=/usr/local/bin:/usr/bin", "SANDBOX_CONFIG_PATH=/app/codeops-runtime.toml", "OPENSANDBOX_SERVER_API_KEY=fixture-lifecycle", "CODEOPS_EXECD_ACCESS_TOKEN=fixture-execd"}, "Cmd": []string{"python", "-m", "uvicorn", "opensandbox_server.main:app", "--host", "0.0.0.0", "--port", "8080", "--no-access-log"}}, "State": map[string]any{"Running": true},
					"HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "codeops-network", "Memory": 512 << 20, "NanoCpus": 2000000000, "PidsLimit": 128, "Tmpfs": map[string]string{"/run/codeops-control": "rw,noexec,nosuid,nodev,size=64m", "/tmp": "rw,noexec,nosuid,nodev,size=64m"}},
					"Mounts":     []map[string]any{{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock", "RW": true}, {"Type": "bind", "Source": serverRoot, "Destination": serverRoot, "RW": false}}},
				sidecarID: map[string]any{"Id": sidecarID, "Config": map[string]any{"Image": egressImage, "Env": []string{"OPENSANDBOX_EGRESS_MODE=dns+nft"}, "Labels": map[string]string{"opensandbox.io/egress-sidecar-for": sandboxID, "codeops.operation_id": strings.Repeat("7", 64), "codeops.opensandbox.policy_sha256": policy}}, "State": map[string]any{"Running": true},
					"HostConfig": map[string]any{"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "CapAdd": []string{"NET_ADMIN"}, "SecurityOpt": []string{"no-new-privileges=true"}, "NetworkMode": "codeops-network", "Memory": 256 << 20, "NanoCpus": 1000000000, "PidsLimit": 64, "Tmpfs": map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=64m"}}},
				taskImage:   map[string]any{"RepoDigests": []string{taskImage}},
				serverImage: map[string]any{"RepoDigests": []string{serverImage}, "Config": map[string]any{"Env": []string{"PATH=/usr/local/bin:/usr/bin", "SANDBOX_CONFIG_PATH=/app/codeops-runtime.toml"}}},
				execdImage:  map[string]any{"RepoDigests": []string{execdImage}},
				egressImage: map[string]any{"RepoDigests": []string{egressImage}},
			}
			if scenario == "writable server root" {
				objects[serverID].(map[string]any)["HostConfig"].(map[string]any)["ReadonlyRootfs"] = false
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
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					if labels["codeops.operation_id"] != strings.Repeat("7", 64) {
						t.Error("runtime effect did not use the caller's persisted operation identity")
					}
					labels["opensandbox.io/id"] = sandboxID
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
					if json.NewDecoder(r.Body).Decode(&body) != nil || !reflect.DeepEqual(body.Argv, []string{"go", "test", "./...", "-count=1"}) || body.Command != "" || body.Cwd != "/workspace" || body.UID != 65534 || body.GID != 65534 || r.Header.Get("X-EXECD-ACCESS-TOKEN") != "fixture-execd" {
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
					if scenario == "incomplete outcome" {
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
					if query.Get("codeops_container_id") != containerID || query.Get("codeops_sidecar_id") != sidecarID || query.Get("codeops_operation_id") != strings.Repeat("7", 64) {
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
			if scenario == "stricter scratch limit" {
				config.TmpfsSize = "16m"
			}
			result, err := sandbox.NewOpenSandboxRunner(config).Run(context.Background(), sandbox.Request{Workspace: root, WorkingDir: root, OperationID: strings.Repeat("7", 64), Program: "go", Args: []string{"test", "./...", "-count=1"}})
			if scenario == "missing execd image" || strings.HasPrefix(scenario, "mismatched local image ID") || scenario == "short local image ID" || scenario == "writable server root" || strings.HasPrefix(scenario, "server ") {
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
				if scenario == "writable root" || scenario == "stricter scratch limit" || scenario == "privileged egress" {
					wantExecuted, wantError = 0, sandbox.ErrSandboxUnavailable
				}
				if !errors.Is(err, wantError) || result.ExitCode != 126 || created != 1 || executed != wantExecuted || deleted != wantDeleted || !strings.Contains(result.RuntimeID, containerID) {
					t.Fatalf("unsafe or unknown outcome admitted: result=%+v lifecycle=%d/%d/%d err=%v", result, created, executed, deleted, err)
				}
				if scenario == "incomplete outcome" && result.Output != "partial failure\n" {
					t.Fatal("partial failure output was discarded")
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
		})
	}
}

func openSandboxDockerFixture(t *testing.T, data string) {
	t.Helper()
	dir := t.TempDir()
	source := `package main
import("os";"encoding/json";"fmt")
func main(){
 a:=os.Args[1:]; if len(a)<2 || (a[0]!="inspect" && !(a[0]=="image" && a[1]=="inspect")){os.Exit(77)}
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
