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
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
		Image  string
		Labels map[string]string
		Env    []string
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

func (r *OpenSandboxRunner) Run(ctx context.Context, request Request) (result Result, resultErr error) {
	result.ExitCode = 126
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Tracer("code-agent/sandbox").Start(ctx, "opensandbox.run")
	defer func() {
		span.SetAttributes(attribute.Int("sandbox.exit_code", result.ExitCode), attribute.Bool("sandbox.output_truncated", result.Truncated), attribute.String("sandbox.runtime_id", result.RuntimeID))
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
		argv = []string{"sh", "-lc", request.Command}
	}
	if strings.TrimSpace(argv[0]) == "" {
		return result, errors.New("sandbox executable is required")
	}
	for _, value := range argv {
		if strings.ContainsRune(value, 0) {
			return result, errors.New("sandbox argv contains NUL")
		}
	}
	cwd, err := ContainerWorkingDir(request.Workspace, request.WorkingDir)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := r.verifyServer(ctx, manifest); err != nil {
		return result, fmt.Errorf("%w: %s", ErrSandboxUnavailable, err)
	}
	var image struct{ RepoDigests []string }
	if r.inspect(ctx, &image, "image", "inspect", r.config.Image) != nil || !openSandboxContains(image.RepoDigests, r.config.Image) {
		return result, fmt.Errorf("%w: approved task image is not present locally", ErrSandboxUnavailable)
	}
	if !openSandboxHex(request.OperationID, 64) {
		return result, fmt.Errorf("%w: OpenSandbox requires a persisted operation identity", ErrSandboxUnavailable)
	}
	operation := request.OperationID
	result.RuntimeID = operation
	metadata := map[string]string{"codeops.operation_id": operation, "codeops.opensandbox.policy_sha256": manifest.PolicySHA256}
	if runtime.GOOS == "windows" && len(workspace) > 2 && workspace[1] == ':' {
		workspace = "/run/desktop/mnt/host/" + strings.ToLower(workspace[:1]) + strings.ReplaceAll(workspace[2:], `\`, "/")
	}
	memory, err := openSandboxMemory(r.config.MemoryLimit)
	if err != nil {
		return result, fmt.Errorf("%w: invalid memory limit", ErrSandboxUnavailable)
	}
	environment := r.environment()
	environment["OPENSANDBOX_EGRESS_MODE"] = "dns+nft"
	create := map[string]any{
		"image": map[string]string{"uri": r.config.Image}, "entrypoint": []string{"sleep", "infinity"}, "timeout": 120,
		"resourceLimits": map[string]string{"cpu": r.config.CPULimit, "memory": strconv.FormatInt(memory, 10)},
		"metadata":       metadata, "env": environment, "networkPolicy": map[string]any{"defaultAction": "deny", "egress": []any{map[string]string{"action": "deny", "target": "0.0.0.0/0"}, map[string]string{"action": "deny", "target": "::/0"}}},
		"volumes": []any{map[string]any{"name": "workspace", "host": map[string]string{"path": workspace}, "mountPath": "/workspace", "readOnly": true}},
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
	if response.StatusCode != http.StatusCreated || openSandboxDecode(response, &created) != nil || !openSandboxUUID(created.ID) {
		response.Body.Close()
		return result, fmt.Errorf("%w: OpenSandbox creation unconfirmed; retain the operation identity", ErrSandboxOutcomeUnknown)
	}
	result.RuntimeID += "/" + created.ID
	var container openSandboxContainer
	if r.inspect(ctx, &container, "inspect", "sandbox-"+created.ID) != nil || !openSandboxHex(container.ID, 64) || container.Config.Labels["opensandbox.io/id"] != created.ID || container.Config.Labels["codeops.operation_id"] != operation {
		return result, fmt.Errorf("%w: OpenSandbox ownership unconfirmed; reconcile the operation identity", ErrSandboxOutcomeUnknown)
	}
	result.RuntimeID += "/" + container.ID
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer stop()
		if err := r.remove(cleanupCtx, manifest, created.ID, operation, container); err != nil {
			result.ExitCode = 126
			resultErr = errors.Join(resultErr, fmt.Errorf("%w: OpenSandbox termination is unconfirmed; reconcile the retained resource identity", ErrSandboxOutcomeUnknown))
		}
	}()
	if !strings.EqualFold(created.Status.State, "Running") || r.verifyTask(ctx, manifest, container, created.ID, workspace, memory) != nil {
		return result, fmt.Errorf("%w: OpenSandbox task isolation is not confirmed", ErrSandboxUnavailable)
	}
	command := map[string]any{"argv": argv, "cwd": cwd, "uid": 65534, "gid": 65534, "timeout": 120000, "envs": r.environment()}
	stream, err := r.call(ctx, manifest, http.MethodPost, "/sandboxes/"+created.ID+"/proxy/44772/command", command)
	if err != nil {
		return result, ErrSandboxOutcomeUnknown
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		return result, ErrSandboxOutcomeUnknown
	}
	output, executionID, truncated, err := openSandboxStream(stream.Body)
	result.Output, result.Truncated = output, truncated
	if err != nil {
		return result, err
	}
	// The pinned execd stream's terminal event does not contain an exit code.
	status, err := r.call(ctx, manifest, http.MethodGet, "/sandboxes/"+created.ID+"/proxy/44772/command/status/"+url.PathEscape(executionID), nil)
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
		if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 && !buffer.truncated && (strings.HasPrefix(strings.TrimSpace(output), "Error: No such object") || strings.HasPrefix(strings.TrimSpace(output), "Error: No such container")) {
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
	if labels["codeops.opensandbox.source_sha"] != manifest.SourceSHA || labels["codeops.opensandbox.policy_sha256"] != manifest.PolicySHA256 || labels["codeops.opensandbox.execd_image"] != manifest.ExecdImage || labels["codeops.opensandbox.egress_image"] != manifest.EgressImage {
		return errors.New("deployed runtime policy differs from approval")
	}
	for _, pin := range []string{manifest.ServerImage, manifest.ExecdImage, manifest.EgressImage} {
		var image struct{ RepoDigests []string }
		if r.inspect(ctx, &image, "image", "inspect", pin) != nil || !openSandboxContains(image.RepoDigests, pin) {
			return errors.New("approved runtime component is not present locally")
		}
	}
	return nil
}

func (r *OpenSandboxRunner) verifyTask(ctx context.Context, manifest openSandboxManifest, task openSandboxContainer, id, workspace string, memory int64) error {
	if task.Config.Labels["opensandbox.io/volume-managed-by"] != "" {
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
	if len(task.Mounts) != 1 || task.Mounts[0].Type != "bind" || task.Mounts[0].Destination != "/workspace" || task.Mounts[0].RW || task.Mounts[0].Source != workspace {
		return ErrSandboxUnavailable
	}
	sidecarID := strings.TrimPrefix(host.NetworkMode, "container:")
	var sidecar openSandboxContainer
	if sidecarID == host.NetworkMode || !openSandboxHex(sidecarID, 64) || r.inspect(ctx, &sidecar, "inspect", sidecarID) != nil || sidecar.ID != sidecarID || sidecar.Config.Image != manifest.EgressImage || !sidecar.State.Running || sidecar.Config.Labels["opensandbox.io/egress-sidecar-for"] != id || !openSandboxContains(sidecar.Config.Env, "OPENSANDBOX_EGRESS_MODE=dns+nft") {
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
		if err == nil && (sidecar.ID != sidecarID || sidecar.Config.Labels["opensandbox.io/egress-sidecar-for"] != id) {
			return ErrSandboxOutcomeUnknown
		}
	} else {
		sidecarID = ""
	}
	if taskExists {
		response, err := r.call(ctx, manifest, http.MethodDelete, "/sandboxes/"+id, nil)
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
