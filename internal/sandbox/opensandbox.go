package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"code-agent/internal/safety"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

const openSandboxSourceSHA = "f89eff24bbd15ae1c37a47769f7667849e679be0"

var errOpenSandboxObjectMissing = errors.New("OpenSandbox owned object is absent")

type openSandboxManifest struct {
	SourceSHA         string `json:"source_sha"`
	Endpoint          string `json:"endpoint"`
	ServerContainerID string `json:"server_container_id"`
	ServerImage       string `json:"server_image"`
	ExecdImage        string `json:"execd_image"`
	EgressImage       string `json:"egress_image"`
	PolicySHA256      string `json:"policy_sha256"`
}

type openSandboxContainer struct {
	ID     string `json:"Id"`
	Config struct {
		Image           string
		WorkingDir      string
		Labels          map[string]string
		Env             []string
		Cmd, Entrypoint []string
	}
	State      struct{ Running bool }
	HostConfig struct {
		ReadonlyRootfs               bool
		Privileged                   bool
		CapAdd, CapDrop, SecurityOpt []string
		NetworkMode                  string
		Memory, NanoCpus, PidsLimit  int64
		Tmpfs                        map[string]string
	}
	Mounts []struct {
		Type, Source, Destination string
		RW                        bool
	}
}

// OpenSandboxRunner keeps the selected runtime behind the Harness Runner seam.
type OpenSandboxRunner struct {
	config Config
	client *http.Client
}

func NewOpenSandboxRunner(config Config) *OpenSandboxRunner {
	return &OpenSandboxRunner{config: normalizeConfig(config), client: &http.Client{
		Transport:     &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("OpenSandbox redirects are refused") },
	}}
}

// Ready checks pinned local prerequisites without creating task resources.
func (r *OpenSandboxRunner) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	manifest, err := r.manifest()
	if err != nil || r.config.AllowWorkspaceWrite || (r.config.Toolchain != "go" && r.config.Toolchain != "python") || !openSandboxImagePin(r.config.Image) {
		return ErrSandboxUnavailable
	}
	if r.verifyServer(ctx, manifest) != nil || !r.imagePresent(ctx, r.config.Image) {
		return ErrSandboxUnavailable
	}
	response, err := r.call(ctx, manifest, http.MethodGet, "/sandboxes", nil)
	if err != nil {
		return ErrSandboxUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrSandboxUnavailable
	}
	return nil
}

func (r *OpenSandboxRunner) Run(ctx context.Context, request Request) (result Result, resultErr error) {
	result.ExitCode = 126
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Tracer("code-agent/sandbox").Start(ctx, "opensandbox.run")
	defer func() {
		span.SetAttributes(attribute.Int("sandbox.exit_code", result.ExitCode), attribute.Bool("sandbox.output_truncated", result.Truncated), attribute.String("sandbox.runtime_id", result.RuntimeID), attribute.Bool("sandbox.stopped", result.Stopped))
		if resultErr != nil || result.ExitCode != 0 {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}()
	if err := validateRequestTrustRoot(request, r.config.TrustRoot, r); err != nil {
		return result, err
	}
	manifest, err := r.manifest()
	if err != nil {
		return result, fmt.Errorf("%w: %s", ErrSandboxUnavailable, err)
	}
	if r.config.AllowWorkspaceWrite || (r.config.Toolchain != "go" && r.config.Toolchain != "python") || !openSandboxImagePin(r.config.Image) || request.Interactive {
		return result, fmt.Errorf("%w: OpenSandbox requires an approved read-only task profile", ErrSandboxUnavailable)
	}
	if r.config.Toolchain == "python" && request.Program == "git" {
		return result, fmt.Errorf("%w: Git is not supported by the approved Python task profile", ErrSandboxUnavailable)
	}
	if request.FrozenInput != nil {
		input := *request.FrozenInput
		input.Files = append([]InputFile(nil), input.Files...)
		request.FrozenInput = &input
		if r.config.Toolchain != "go" {
			return result, fmt.Errorf("%w: frozen input requires the approved Go profile", ErrSandboxUnavailable)
		}
		release, err := pinFrozenInput(ctx, r.config.TrustRoot, request)
		if err != nil {
			return result, fmt.Errorf("%w: %s", ErrSandboxUnavailable, err)
		}
		defer release()
	}
	dependency := ""
	if r.config.Toolchain == "python" && request.DependencyOperationID == "" {
		return result, fmt.Errorf("%w: approved Python dependency identity is required", ErrSandboxUnavailable)
	}
	if request.DependencyOperationID != "" {
		if r.config.Toolchain != "python" || !openSandboxHex(request.DependencyOperationID, 64) {
			return result, fmt.Errorf("%w: approved Python dependency identity is required", ErrSandboxUnavailable)
		}
		dependency = filepath.Join(r.config.TrustRoot, "dependency-"+request.DependencyOperationID, PytzName)
		release, err := safety.PinReadOnlyFiles(dependency)
		if err != nil {
			return result, ErrSandboxUnavailable
		}
		defer release()
		if ValidatePytz(ctx, r.config.TrustRoot, request.DependencyOperationID) != nil {
			return result, ErrSandboxUnavailable
		}
		dependency = openSandboxHostPath(dependency)
	}
	cpu, err := strconv.ParseFloat(r.config.CPULimit, 64)
	if err != nil || math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu <= 0 || cpu > 64 {
		return result, fmt.Errorf("%w: invalid CPU limit", ErrSandboxUnavailable)
	}
	workspace, err := absolutePath(request.Workspace)
	if err != nil {
		return result, err
	}
	argv := append([]string{request.Program}, request.Args...)
	if strings.TrimSpace(request.Command) != "" {
		if request.Program != "" {
			return result, errors.New("sandbox request must contain exactly one of command or program")
		}
		argv = []string{"sh", "-c", request.Command}
	}
	if strings.TrimSpace(argv[0]) == "" {
		return result, errors.New("sandbox executable is required")
	}
	for _, value := range argv {
		if strings.ContainsRune(value, 0) {
			return result, errors.New("sandbox argv contains NUL")
		}
	}
	cwd := ""
	if request.FrozenInput != nil {
		cwd = "/run/codeops/input-" + request.OperationID
	} else {
		cwd, err = ContainerWorkingDir(request.Workspace, request.WorkingDir)
		if err != nil {
			return result, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := r.verifyServer(ctx, manifest); err != nil {
		return result, fmt.Errorf("%w: %s", ErrSandboxUnavailable, err)
	}
	if !r.imagePresent(ctx, r.config.Image) {
		return result, fmt.Errorf("%w: approved task image is not present locally", ErrSandboxUnavailable)
	}
	if !openSandboxHex(request.OperationID, 64) {
		return result, fmt.Errorf("%w: OpenSandbox requires a persisted operation identity", ErrSandboxUnavailable)
	}
	operation := request.OperationID
	result.RuntimeID = operation
	metadata := map[string]string{"codeops.operation_id": operation, "codeops.opensandbox.policy_sha256": manifest.PolicySHA256}
	workspace = openSandboxHostPath(workspace)
	memory, err := openSandboxMemory(r.config.MemoryLimit)
	if err != nil {
		return result, fmt.Errorf("%w: invalid memory limit", ErrSandboxUnavailable)
	}
	environment := r.environment()
	create := map[string]any{
		"image": map[string]string{"uri": r.config.Image}, "entrypoint": []string{"sleep", "infinity"}, "timeout": 120,
		"resourceLimits": map[string]string{"cpu": r.config.CPULimit, "memory": strconv.FormatInt(memory, 10)},
		"metadata":       metadata, "env": environment, "networkPolicy": map[string]any{"defaultAction": "deny", "egress": []any{map[string]string{"action": "deny", "target": "0.0.0.0/0"}, map[string]string{"action": "deny", "target": "::/0"}}},
		"volumes": []any{map[string]any{"name": "workspace", "host": map[string]string{"path": workspace}, "mountPath": "/workspace", "readOnly": true}},
	}
	if dependency != "" {
		create["volumes"] = append(create["volumes"].([]any), map[string]any{"name": "dependency", "host": map[string]string{"path": dependency}, "mountPath": "/deps/" + PytzName, "readOnly": true})
	}
	response, err := r.call(ctx, manifest, http.MethodPost, "/sandboxes", create)
	if err != nil {
		return result, fmt.Errorf("%w: OpenSandbox creation unconfirmed; retain the operation identity", ErrSandboxOutcomeUnknown)
	}
	var created struct {
		ID     string `json:"id"`
		Status struct {
			State string `json:"state"`
		}
	}
	if response.StatusCode != http.StatusAccepted || openSandboxDecode(response, &created) != nil || !openSandboxUUID(created.ID) {
		response.Body.Close()
		return result, fmt.Errorf("%w: OpenSandbox creation unconfirmed; retain the operation identity", ErrSandboxOutcomeUnknown)
	}
	result.RuntimeID += "/" + created.ID
	var container openSandboxContainer
	ownershipCtx, stopOwnership := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	ownershipErr := r.inspect(ownershipCtx, &container, "inspect", "sandbox-"+created.ID)
	stopOwnership()
	if ownershipErr != nil || !openSandboxHex(container.ID, 64) || container.Config.Labels["opensandbox.io/id"] != created.ID || container.Config.Labels["codeops.operation_id"] != operation {
		return result, fmt.Errorf("%w: OpenSandbox ownership unconfirmed; reconcile the operation identity", ErrSandboxOutcomeUnknown)
	}
	result.RuntimeID += "/" + container.ID
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer stop()
		if err := r.remove(cleanupCtx, manifest, created.ID, operation, container); err != nil {
			result.ExitCode = 126
			result.InputSHA256 = ""
			resultErr = errors.Join(resultErr, fmt.Errorf("%w: OpenSandbox termination is unconfirmed; reconcile the retained resource identity", ErrSandboxOutcomeUnknown))
		} else {
			result.Stopped = true
		}
	}()
	if ctx.Err() != nil {
		return result, fmt.Errorf("%w: request ended before command dispatch", ErrSandboxOutcomeUnknown)
	}
	if !strings.EqualFold(created.Status.State, "Running") || r.verifyTask(ctx, manifest, container, created.ID, workspace, dependency, memory) != nil {
		return result, fmt.Errorf("%w: OpenSandbox task isolation is not confirmed", ErrSandboxUnavailable)
	}
	if request.FrozenInput != nil {
		// Bootstrap only unpacks verified regular source files; model code first runs as nobody.
		bootstrap := "set -eu; printf '%s  /workspace\\n' '" + request.FrozenInput.ArchiveSHA256 + "' | sha256sum -c -; mkdir '" + cwd + "'; tar --no-same-owner -xf /workspace -C '" + cwd + "'; find '" + cwd + "' -type d -exec chmod 0555 {} +"
		prepared, err := r.execute(ctx, manifest, created.ID, []string{"sh", "-c", bootstrap}, "/run/codeops", 0)
		result.Output, result.Truncated = prepared.Output, prepared.Truncated
		if err != nil || prepared.ExitCode != 0 || prepared.Truncated {
			if err == nil {
				err = fmt.Errorf("%w: frozen input bootstrap failed", ErrSandboxUnavailable)
			}
			return result, err
		}
	}
	executed, err := r.execute(ctx, manifest, created.ID, argv, cwd, 65534)
	result.Output, result.Truncated, result.ExitCode = executed.Output, executed.Truncated, executed.ExitCode
	if err == nil && request.FrozenInput != nil {
		result.InputSHA256 = request.FrozenInput.SourceSHA256
	}
	return result, err
}

func (r *OpenSandboxRunner) execute(ctx context.Context, manifest openSandboxManifest, id string, argv []string, cwd string, uid int) (result Result, resultErr error) {
	result.ExitCode = 126
	command := map[string]any{"argv": argv, "cwd": cwd, "uid": uid, "gid": uid, "timeout": 120000, "envs": r.environment()}
	stream, err := r.call(ctx, manifest, http.MethodPost, "/sandboxes/"+id+"/proxy/44772/command", command)
	if err != nil {
		return result, ErrSandboxOutcomeUnknown
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		return result, ErrSandboxOutcomeUnknown
	}
	output, executionID, truncated, err := openSandboxStream(stream.Body)
	result.Output, result.Truncated = output, truncated
	if err != nil && output != "" {
		result.Truncated = true
	}
	if err != nil {
		return result, err
	}
	// The pinned execd stream's terminal event does not contain an exit code.
	status, err := r.call(ctx, manifest, http.MethodGet, "/sandboxes/"+id+"/proxy/44772/command/status/"+url.PathEscape(executionID), nil)
	if err != nil {
		return result, ErrSandboxOutcomeUnknown
	}
	var outcome struct {
		ID       string `json:"id"`
		Running  *bool  `json:"running"`
		ExitCode *int   `json:"exit_code"`
	}
	if status.StatusCode != http.StatusOK {
		status.Body.Close()
		return result, ErrSandboxOutcomeUnknown
	}
	if openSandboxDecode(status, &outcome) != nil || outcome.ID != executionID || outcome.Running == nil || *outcome.Running || outcome.ExitCode == nil || *outcome.ExitCode < 0 || *outcome.ExitCode > 255 {
		return result, ErrSandboxOutcomeUnknown
	}
	result.ExitCode = *outcome.ExitCode
	return result, nil
}

func (r *OpenSandboxRunner) manifest() (openSandboxManifest, error) {
	var manifest openSandboxManifest
	if !openSandboxHex(r.config.OpenSandboxManifestSHA256, 64) {
		return manifest, errors.New("approved runtime manifest is required")
	}
	file, err := os.Open(r.config.OpenSandboxManifest)
	if err != nil {
		return manifest, errors.New("approved runtime manifest is unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return manifest, errors.New("runtime manifest exceeds its bound")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != r.config.OpenSandboxManifestSHA256 {
		return manifest, errors.New("runtime manifest checksum differs from approval")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.SourceSHA != openSandboxSourceSHA || !openSandboxHex(manifest.ServerContainerID, 64) || !openSandboxHex(manifest.PolicySHA256, 64) || !openSandboxImagePin(manifest.ServerImage) || !openSandboxImagePin(manifest.ExecdImage) || !openSandboxImagePin(manifest.EgressImage) {
		return manifest, errors.New("approved runtime identities are incomplete")
	}
	endpoint, err := url.Parse(manifest.Endpoint)
	if err != nil {
		return manifest, errors.New("invalid runtime endpoint")
	}
	ip := net.ParseIP(endpoint.Hostname())
	port, err := strconv.Atoi(endpoint.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Path != "/v1" || endpoint.RawPath != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return manifest, errors.New("runtime endpoint must be an explicit local private endpoint")
	}
	for _, name := range []string{"CODE_AGENT_OPENSANDBOX_API_KEY", "CODE_AGENT_OPENSANDBOX_EXECD_TOKEN"} {
		value := os.Getenv(name)
		if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			return manifest, errors.New("runtime credentials are unavailable")
		}
	}
	return manifest, nil
}

func (r *OpenSandboxRunner) call(ctx context.Context, manifest openSandboxManifest, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, manifest.Endpoint+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("OPEN-SANDBOX-API-KEY", os.Getenv("CODE_AGENT_OPENSANDBOX_API_KEY"))
	request.Header.Set("X-EXECD-ACCESS-TOKEN", os.Getenv("CODE_AGENT_OPENSANDBOX_EXECD_TOKEN"))
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	return r.client.Do(request)
}

func openSandboxDecode(response *http.Response, value any) error {
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return ErrSandboxOutcomeUnknown
	}
	return json.Unmarshal(data, value)
}

func (r *OpenSandboxRunner) inspect(ctx context.Context, value any, args ...string) error {
	args = append(append(append([]string(nil), args[:len(args)-1]...), "--format", "{{json .}}"), args[len(args)-1])
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = safety.ScrubEnvironment(os.Environ())
	command.WaitDelay = 5 * time.Second
	buffer := &boundedBuffer{}
	command.Stdout, command.Stderr = buffer, buffer
	err := command.Run()
	output := buffer.String()
	if err != nil {
		var exit *exec.ExitError
		diagnostic := strings.ToLower(strings.TrimSpace(output))
		if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 && !buffer.truncated && (strings.HasPrefix(diagnostic, "error: no such object") || strings.HasPrefix(diagnostic, "error: no such container")) {
			return errOpenSandboxObjectMissing
		}
		return ErrSandboxOutcomeUnknown
	}
	if buffer.truncated || json.Unmarshal([]byte(output), value) != nil {
		return ErrSandboxOutcomeUnknown
	}
	return nil
}

func (r *OpenSandboxRunner) verifyServer(ctx context.Context, manifest openSandboxManifest) error {
	var server openSandboxContainer
	if r.inspect(ctx, &server, "inspect", manifest.ServerContainerID) != nil || server.ID != manifest.ServerContainerID || server.Config.Image != manifest.ServerImage || !server.State.Running {
		return errors.New("approved runtime server identity is unavailable")
	}
	labels := server.Config.Labels
	toolchain := labels["codeops.opensandbox.toolchain"]
	if toolchain == "" {
		toolchain = "go"
	}
	if toolchain != r.config.Toolchain {
		return errors.New("runtime toolchain differs from approval")
	}
	if labels["codeops.opensandbox.source_sha"] != manifest.SourceSHA || labels["codeops.opensandbox.policy_sha256"] != manifest.PolicySHA256 || labels["codeops.opensandbox.execd_image"] != manifest.ExecdImage || labels["codeops.opensandbox.egress_image"] != manifest.EgressImage {
		return errors.New("deployed runtime policy differs from approval")
	}
	host := server.HostConfig
	if !host.ReadonlyRootfs || host.Privileged || len(host.CapAdd) != 0 || !openSandboxContains(host.CapDrop, "ALL") || len(host.SecurityOpt) != 1 || host.SecurityOpt[0] != "no-new-privileges=true" || host.Memory <= 0 || host.Memory > 512<<20 || host.NanoCpus <= 0 || host.NanoCpus > 2e9 || host.PidsLimit <= 0 || host.PidsLimit > 128 || host.NetworkMode == "host" || host.NetworkMode == "none" || host.NetworkMode == "" || strings.HasPrefix(host.NetworkMode, "container:") {
		return errors.New("runtime control plane isolation differs from approval")
	}
	if len(host.Tmpfs) != 2 || !openSandboxTmpfs(host.Tmpfs["/run/codeops-control"], 64<<20, false) || !openSandboxTmpfs(host.Tmpfs["/tmp"], 64<<20, false) {
		return errors.New("runtime control plane scratch differs from approval")
	}
	root := openSandboxHostPath(r.config.TrustRoot)
	socket, workspace := false, false
	for _, mount := range server.Mounts {
		if mount.Type == "bind" && mount.Source == "/var/run/docker.sock" && mount.Destination == "/var/run/docker.sock" && mount.RW {
			socket = true
		} else if mount.Type == "bind" && mount.Source == root && mount.Destination == root && !mount.RW {
			workspace = true
		}
	}
	if len(server.Mounts) != 2 || !socket || !workspace || server.Config.WorkingDir != "/app" || len(server.Config.Entrypoint) != 0 || strings.Join(server.Config.Cmd, "\x00") != "python\x00-m\x00uvicorn\x00opensandbox_server.main:app\x00--host\x000.0.0.0\x00--port\x008080\x00--no-access-log" {
		return errors.New("runtime control plane inputs differ from approval")
	}
	var image struct{ Config struct{ Env []string } }
	if r.inspect(ctx, &image, "image", "inspect", manifest.ServerImage) != nil {
		return errors.New("approved runtime image configuration is unavailable")
	}
	expected, actual := map[string]string{}, map[string]string{}
	for index, entries := range [][]string{image.Config.Env, server.Config.Env} {
		values := expected
		if index == 1 {
			values = actual
		}
		for _, entry := range entries {
			key, value, valid := strings.Cut(entry, "=")
			if _, duplicate := values[key]; !valid || key == "" || duplicate {
				return errors.New("runtime control plane environment is ambiguous")
			}
			values[key] = value
		}
	}
	expected["SANDBOX_CONFIG_PATH"] = "/app/codeops-runtime.toml"
	expected["OPENSANDBOX_SERVER_API_KEY"] = os.Getenv("CODE_AGENT_OPENSANDBOX_API_KEY")
	expected["CODEOPS_EXECD_ACCESS_TOKEN"] = os.Getenv("CODE_AGENT_OPENSANDBOX_EXECD_TOKEN")
	if !maps.Equal(expected, actual) {
		return errors.New("runtime control plane configuration differs from approval")
	}
	for _, pin := range []string{manifest.ServerImage, manifest.ExecdImage, manifest.EgressImage} {
		if !r.imagePresent(ctx, pin) {
			return errors.New("approved runtime component is not present locally")
		}
	}
	return nil
}

func openSandboxHostPath(path string) string {
	if runtime.GOOS == "windows" && len(path) > 2 && path[1] == ':' {
		return "/run/desktop/mnt/host/" + strings.ToLower(path[:1]) + strings.ReplaceAll(path[2:], `\`, "/")
	}
	return path
}

func (r *OpenSandboxRunner) imagePresent(ctx context.Context, pin string) bool {
	var image struct {
		ID          string `json:"Id"`
		RepoDigests []string
	}
	if r.inspect(ctx, &image, "image", "inspect", pin) != nil {
		return false
	}
	if strings.HasPrefix(pin, "sha256:") && !strings.Contains(pin, "@") {
		return image.ID == pin
	}
	return openSandboxContains(image.RepoDigests, pin)
}

func (r *OpenSandboxRunner) verifyTask(ctx context.Context, manifest openSandboxManifest, task openSandboxContainer, id, workspace, dependency string, memory int64) error {
	if task.Config.Labels["opensandbox.io/volume-managed-by"] != "" || task.Config.Labels["codeops.opensandbox.policy_sha256"] != manifest.PolicySHA256 {
		return ErrSandboxUnavailable
	}
	host := task.HostConfig
	cpu, err := strconv.ParseFloat(r.config.CPULimit, 64)
	if err != nil || math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu <= 0 || cpu > 64 {
		return ErrSandboxUnavailable
	}
	if task.Config.Image != r.config.Image || !task.State.Running || !host.ReadonlyRootfs || host.Privileged || !openSandboxContains(host.CapDrop, "ALL") || !openSandboxContains(host.SecurityOpt, "no-new-privileges=true") || host.Memory <= 0 || host.Memory > memory || host.NanoCpus <= 0 || host.NanoCpus > int64(cpu*1e9) || host.PidsLimit <= 0 || host.PidsLimit > int64(r.config.PidsLimit) {
		return ErrSandboxUnavailable
	}
	for _, cap := range host.CapAdd {
		if cap != "SETUID" && cap != "SETGID" {
			return ErrSandboxUnavailable
		}
	}
	for _, option := range host.SecurityOpt {
		if option != "no-new-privileges=true" {
			return ErrSandboxUnavailable
		}
	}
	tmpfsLimit, err := openSandboxMemory(r.config.TmpfsSize)
	if err != nil || len(host.Tmpfs) != 2 || !openSandboxTmpfs(host.Tmpfs["/run/codeops"], 512<<20, true) || !openSandboxTmpfs(host.Tmpfs["/tmp"], min(tmpfsLimit, 64<<20), false) {
		return ErrSandboxUnavailable
	}
	workspaceMount, dependencyMount := false, dependency == ""
	for _, mount := range task.Mounts {
		if mount.Type != "bind" || mount.RW {
			return ErrSandboxUnavailable
		}
		if mount.Destination == "/workspace" && mount.Source == workspace {
			workspaceMount = true
		}
		if dependency != "" && mount.Destination == "/deps/"+PytzName && mount.Source == dependency {
			dependencyMount = true
		}
	}
	expectedMounts := 1
	if dependency != "" {
		expectedMounts = 2
	}
	if len(task.Mounts) != expectedMounts || !workspaceMount || !dependencyMount {
		return ErrSandboxUnavailable
	}
	sidecarID := strings.TrimPrefix(host.NetworkMode, "container:")
	var sidecar openSandboxContainer
	if sidecarID == host.NetworkMode || !openSandboxHex(sidecarID, 64) || r.inspect(ctx, &sidecar, "inspect", sidecarID) != nil || sidecar.ID != sidecarID || sidecar.Config.Image != manifest.EgressImage || !sidecar.State.Running || sidecar.Config.Labels["opensandbox.io/egress-sidecar-for"] != id || !openSandboxContains(sidecar.Config.Env, "OPENSANDBOX_EGRESS_MODE=dns+nft") {
		return ErrSandboxUnavailable
	}
	var server openSandboxContainer
	egress := sidecar.HostConfig
	if r.inspect(ctx, &server, "inspect", manifest.ServerContainerID) != nil || server.ID != manifest.ServerContainerID || egress.NetworkMode != server.HostConfig.NetworkMode || sidecar.Config.Labels["codeops.operation_id"] != task.Config.Labels["codeops.operation_id"] || sidecar.Config.Labels["codeops.opensandbox.policy_sha256"] != manifest.PolicySHA256 || !egress.ReadonlyRootfs || egress.Privileged || len(egress.CapAdd) != 1 || egress.CapAdd[0] != "NET_ADMIN" || !openSandboxContains(egress.CapDrop, "ALL") || len(egress.SecurityOpt) != 1 || egress.SecurityOpt[0] != "no-new-privileges=true" || egress.Memory <= 0 || egress.Memory > 256<<20 || egress.NanoCpus <= 0 || egress.NanoCpus > 1e9 || egress.PidsLimit <= 0 || egress.PidsLimit > 64 || len(sidecar.Mounts) != 0 || len(egress.Tmpfs) != 1 || !openSandboxTmpfs(egress.Tmpfs["/tmp"], 64<<20, false) {
		return ErrSandboxUnavailable
	}
	return nil
}

func (r *OpenSandboxRunner) remove(ctx context.Context, manifest openSandboxManifest, id, operation string, task openSandboxContainer) error {
	var owned openSandboxContainer
	err := r.inspect(ctx, &owned, "inspect", task.ID)
	if err != nil && !errors.Is(err, errOpenSandboxObjectMissing) {
		return err
	}
	if err == nil && (owned.ID != task.ID || owned.Config.Labels["opensandbox.io/id"] != id || owned.Config.Labels["codeops.operation_id"] != operation) {
		return ErrSandboxOutcomeUnknown
	}
	taskExists := err == nil
	var bound openSandboxContainer
	bindingErr := r.inspect(ctx, &bound, "inspect", "sandbox-"+id)
	if taskExists {
		if bindingErr != nil || bound.ID != task.ID || bound.Config.Labels["opensandbox.io/id"] != id || bound.Config.Labels["codeops.operation_id"] != operation {
			return ErrSandboxOutcomeUnknown
		}
	} else if !errors.Is(bindingErr, errOpenSandboxObjectMissing) {
		return ErrSandboxOutcomeUnknown
	}
	// Lifecycle deletion also removes declared volumes; unsupported resources require reconciliation.
	if task.Config.Labels["opensandbox.io/volume-managed-by"] != "" || owned.Config.Labels["opensandbox.io/volume-managed-by"] != "" || bound.Config.Labels["opensandbox.io/volume-managed-by"] != "" {
		return ErrSandboxOutcomeUnknown
	}
	sidecarID := strings.TrimPrefix(task.HostConfig.NetworkMode, "container:")
	if sidecarID != task.HostConfig.NetworkMode && openSandboxHex(sidecarID, 64) {
		var sidecar openSandboxContainer
		err = r.inspect(ctx, &sidecar, "inspect", sidecarID)
		if err != nil && !errors.Is(err, errOpenSandboxObjectMissing) {
			return err
		}
		if err == nil && (sidecar.ID != sidecarID || sidecar.Config.Labels["opensandbox.io/egress-sidecar-for"] != id || sidecar.Config.Labels["codeops.operation_id"] != operation || sidecar.Config.Labels["codeops.opensandbox.policy_sha256"] != manifest.PolicySHA256) {
			return ErrSandboxOutcomeUnknown
		}
	} else {
		sidecarID = ""
	}
	if taskExists {
		query := url.Values{"codeops_container_id": {task.ID}, "codeops_sidecar_id": {sidecarID}, "codeops_operation_id": {operation}}
		response, err := r.call(ctx, manifest, http.MethodDelete, "/sandboxes/"+id+"?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
			return ErrSandboxOutcomeUnknown
		}
	}
	for {
		response, err := r.call(ctx, manifest, http.MethodGet, "/sandboxes/"+id, nil)
		if err != nil {
			return err
		}
		response.Body.Close()
		var value openSandboxContainer
		taskErr := r.inspect(ctx, &value, "inspect", task.ID)
		bindingErr := r.inspect(ctx, &value, "inspect", "sandbox-"+id)
		sidecarErr := errOpenSandboxObjectMissing
		if sidecarID != "" {
			sidecarErr = r.inspect(ctx, &value, "inspect", sidecarID)
		}
		if response.StatusCode == http.StatusNotFound && errors.Is(taskErr, errOpenSandboxObjectMissing) && errors.Is(bindingErr, errOpenSandboxObjectMissing) && errors.Is(sidecarErr, errOpenSandboxObjectMissing) {
			return nil
		}
		if (taskErr != nil && !errors.Is(taskErr, errOpenSandboxObjectMissing)) || (bindingErr != nil && !errors.Is(bindingErr, errOpenSandboxObjectMissing)) || (sidecarErr != nil && !errors.Is(sidecarErr, errOpenSandboxObjectMissing)) {
			return ErrSandboxOutcomeUnknown
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *OpenSandboxRunner) environment() map[string]string {
	env := map[string]string{"HOME": "/run/codeops", "TMPDIR": "/run/codeops"}
	if r.config.Toolchain == "go" {
		for key, value := range map[string]string{"GOCACHE": "/run/codeops/go-build", "GOPATH": "/run/codeops/go", "GOTOOLCHAIN": "local", "GOENV": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"} {
			env[key] = value
		}
	} else {
		for _, key := range []string{"PYTHONDONTWRITEBYTECODE", "PYTHONNOUSERSITE", "PIP_NO_INDEX", "PIP_DISABLE_PIP_VERSION_CHECK"} {
			env[key] = "1"
		}
		env["PYTHONPATH"] = "/run/codeops/python-site"
	}
	return env
}

func openSandboxStream(reader io.Reader) (string, string, bool, error) {
	buffer := &boundedBuffer{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 65536)
	id := ""
	complete := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || (complete && event.Type != "ping") {
			return buffer.String(), id, buffer.truncated, ErrSandboxOutcomeUnknown
		}
		switch event.Type {
		case "init":
			if id != "" || event.Text == "" || len(event.Text) > 128 || strings.ContainsAny(event.Text, "/\\\r\n\x00") {
				return buffer.String(), id, buffer.truncated, ErrSandboxOutcomeUnknown
			}
			id = event.Text
		case "stdout", "stderr":
			if id == "" {
				return buffer.String(), id, buffer.truncated, ErrSandboxOutcomeUnknown
			}
			_, _ = buffer.Write([]byte(event.Text))
		case "execution_complete", "error":
			if id == "" {
				return buffer.String(), id, buffer.truncated, ErrSandboxOutcomeUnknown
			}
			complete = true
		case "ping", "status":
		default:
			return buffer.String(), id, buffer.truncated, ErrSandboxOutcomeUnknown
		}
	}
	if scanner.Err() != nil {
		return buffer.String(), id, true, ErrSandboxOutcomeUnknown
	}
	if !complete {
		return buffer.String(), id, buffer.truncated, ErrSandboxOutcomeUnknown
	}
	return buffer.String(), id, buffer.truncated, nil
}

func openSandboxContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func openSandboxHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func openSandboxImagePin(image string) bool {
	if strings.HasPrefix(image, "sha256:") && !strings.Contains(image, "@") {
		return openSandboxHex(strings.TrimPrefix(image, "sha256:"), 64)
	}
	parts := strings.Split(image, "@sha256:")
	return len(parts) == 2 && parts[0] != "" && !strings.HasPrefix(parts[0], "-") && !strings.ContainsAny(parts[0], " \t\r\n\x00") && openSandboxHex(parts[1], 64)
}

func openSandboxUUID(id string) bool {
	return len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' && openSandboxHex(strings.ReplaceAll(id, "-", ""), 32)
}

func openSandboxMemory(value string) (int64, error) {
	multiplier := int64(1)
	if value != "" {
		switch value[len(value)-1] {
		case 'k':
			multiplier = 1 << 10
		case 'm':
			multiplier = 1 << 20
		case 'g':
			multiplier = 1 << 30
		}
	}
	if multiplier != 1 {
		value = value[:len(value)-1]
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/multiplier {
		return 0, errors.New("invalid memory bound")
	}
	return n * multiplier, nil
}

func openSandboxTmpfs(options string, limit int64, executable bool) bool {
	flags := strings.Split(options, ",")
	if !openSandboxContains(flags, "rw") || !openSandboxContains(flags, "nosuid") || !openSandboxContains(flags, "nodev") || !openSandboxContains(flags, map[bool]string{true: "exec", false: "noexec"}[executable]) {
		return false
	}
	size := int64(0)
	for _, flag := range flags {
		switch flag {
		case "rw", "nosuid", "nodev", "mode=1777":
		case "exec":
			if !executable {
				return false
			}
		case "noexec":
			if executable {
				return false
			}
		default:
			if size != 0 || !strings.HasPrefix(flag, "size=") {
				return false
			}
			var err error
			size, err = openSandboxMemory(strings.TrimPrefix(flag, "size="))
			if err != nil {
				return false
			}
		}
	}
	return size > 0 && size <= limit && (!executable || openSandboxContains(flags, "mode=1777"))
}
