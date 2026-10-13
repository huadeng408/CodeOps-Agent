package admission

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ProviderConfig struct {
	Protocol    string
	BaseURL     string
	Model       string
	APIKey      string
	InputLimit  int64
	OutputLimit int64
	TotalLimit  int64 // Optional accounting bound when the answer cap excludes reasoning.
}

type modelScope struct {
	owner  uint
	taskID string
	id     string
}

// Gateway owns upstream transport and accounting; Python supplies the
// provider payload using its existing model-policy adapters.
type Gateway struct {
	pb.UnimplementedModelGatewayServer
	budget        *Budget
	provider      ProviderConfig
	mu            sync.Mutex
	scopes        map[[32]byte]modelScope
	server        *grpc.Server
	listener      net.Listener
	address       string
	http          *http.Client
	taskAdmission func(context.Context, string) error
}

func NewGateway(budget *Budget, provider ProviderConfig) *Gateway {
	return &Gateway{budget: budget, provider: provider, scopes: make(map[[32]byte]modelScope), http: &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirects are not admitted") },
	}}
}

// SetTaskAdmission is configured by Go; the capability's Ledger root supplies the task ID.
func (gateway *Gateway) SetTaskAdmission(check func(context.Context, string) error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.taskAdmission = check
}

func (gateway *Gateway) Start() error {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.server != nil {
		return errors.New("model gateway already started")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	gateway.listener, gateway.address = listener, listener.Addr().String()
	gateway.server = grpc.NewServer(grpc.MaxRecvMsgSize(8<<20), grpc.MaxSendMsgSize(16<<20), grpc.WaitForHandlers(true))
	pb.RegisterModelGatewayServer(gateway.server, gateway)
	go func() { _ = gateway.server.Serve(listener) }()
	return nil
}

func (gateway *Gateway) Close() {
	gateway.mu.Lock()
	scopes := gateway.scopes
	gateway.scopes = make(map[[32]byte]modelScope)
	server, listener := gateway.server, gateway.listener
	// Stop outside the mutex: a canceled handler may still need its scope.
	gateway.address = ""
	gateway.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	if listener != nil {
		_ = listener.Close()
	}
	for _, scope := range scopes {
		gateway.releaseScope(scope)
	}
}

func (gateway *Gateway) releaseScope(scope modelScope) {
	if gateway.budget != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// A failed append retains the cross-process fence for explicit recovery.
		_ = gateway.budget.recordScope(ctx, scope.owner, scope.taskID, scope.id, "admission/scope_released")
	}
}

func (gateway *Gateway) Bind(owner uint, taskID, sessionID string) (*pb.ModelGatewayBinding, func(), error) {
	if owner == 0 || !validID(taskID) || !validID(sessionID) {
		return nil, nil, ErrInvalidAdmission
	}
	capability := make([]byte, 32)
	if _, err := rand.Read(capability); err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(capability)
	scope := modelScope{owner: owner, taskID: taskID, id: hex.EncodeToString(digest[:])}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.address == "" {
		return nil, nil, errors.New("model gateway unavailable")
	}
	if gateway.budget != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := gateway.budget.recordScope(ctx, owner, taskID, scope.id, session.VerificationAdmissionScopeBoundEventType)
		cancel()
		if err != nil {
			return nil, nil, err
		}
	}
	gateway.scopes[digest] = scope
	release := func() {
		gateway.mu.Lock()
		defer gateway.mu.Unlock()
		if _, exists := gateway.scopes[digest]; exists {
			delete(gateway.scopes, digest)
			gateway.releaseScope(scope)
		}
	}
	return &pb.ModelGatewayBinding{SchemaVersion: 1, Address: gateway.address, Capability: capability,
		Protocol: gateway.provider.Protocol, Model: gateway.provider.Model, OutputLimit: gateway.provider.OutputLimit}, release, nil
}

// BindActor accepts only the Harness-validated actor, never model-supplied identity.
func (gateway *Gateway) BindActor(actor identity.Actor, _ string) (*pb.ModelGatewayBinding, func(), error) {
	if gateway.budget == nil {
		return nil, nil, ErrInvalidAdmission
	}
	if err := actor.Validate(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	taskID, err := session.ModelTaskRoot(ctx, gateway.budget.ledger, actor)
	if err != nil {
		return nil, nil, err
	}
	return gateway.Bind(actorOwner(actor), taskID, actor.SessionID)
}

func actorOwner(actor identity.Actor) uint {
	digest := sha256.Sum256([]byte(actor.TenantID + "\x00" + actor.ActorID + "\x00" + actor.Subject))
	owner := uint(binary.BigEndian.Uint64(digest[:8]) & ((1 << 53) - 1))
	if owner == 0 {
		return 1
	}
	return owner
}

// BindExistingTask fences non-model execution inside an already admitted task.
// The caller must first authorize the public Session owner and repository.
func (gateway *Gateway) BindExistingTask(ctx context.Context, owner uint, taskID string) (func() error, error) {
	if gateway.budget == nil || owner == 0 || !validID(taskID) {
		return nil, ErrInvalidAdmission
	}
	state, err := gateway.budget.read(ctx, owner)
	if err != nil {
		return nil, err
	}
	if state.ActiveCalls != 0 {
		return nil, ErrUsageUnknown
	}
	if state.TaskID != taskID || state.TaskOwnerID == 0 {
		return nil, ErrTaskBusy
	}
	binding, release, err := gateway.Bind(state.TaskOwnerID, taskID, taskID)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(binding.Capability)
	scopeID := hex.EncodeToString(digest[:])
	return func() error {
		release()
		checkCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		current, err := gateway.budget.read(checkCtx, state.TaskOwnerID)
		if err != nil {
			return err
		}
		if _, held := current.scopes[scopeID]; held {
			return ErrTaskBusy
		}
		return nil
	}, nil
}

// FinishTask is an explicit operator action, never a per-turn or restart reset.
func (gateway *Gateway) FinishTask(ctx context.Context, actor identity.Actor) error {
	if gateway.budget == nil || actor.Validate() != nil {
		return ErrInvalidAdmission
	}
	root, err := session.ModelTaskRoot(ctx, gateway.budget.ledger, actor)
	if err != nil {
		return err
	}
	if root != actor.SessionID {
		return ErrInvalidAdmission
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if err := session.ModelTaskIdle(ctx, gateway.budget.ledger, actor); err != nil {
		return err
	}
	for _, scope := range gateway.scopes {
		if scope.taskID == root {
			return ErrTaskBusy
		}
	}
	state, err := gateway.budget.read(ctx, actorOwner(actor))
	if err != nil {
		return err
	}
	if state.closedTasks[taskKey(actorOwner(actor), root)] {
		return nil
	}
	if state.taskHasScopes(actorOwner(actor), root) || state.outstanding() != 0 {
		if err := gateway.budget.recoverScopes(ctx, actorOwner(actor), root); err != nil {
			return err
		}
	}
	if state.TaskID == "" {
		if _, err := gateway.budget.AcquireTask(ctx, actorOwner(actor), root); err != nil {
			return err
		}
	}
	_, err = gateway.budget.FinishTask(ctx, actorOwner(actor), root)
	return err
}

func (gateway *Gateway) Invoke(ctx context.Context, request *pb.ModelCallRequest) (*pb.ModelCallResponse, error) {
	if request == nil || len(request.Capability) != 32 {
		return nil, status.Error(codes.Unauthenticated, "model capability required")
	}
	digest := sha256.Sum256(request.Capability)
	gateway.mu.Lock()
	scope, found := gateway.scopes[digest]
	checkTask := gateway.taskAdmission
	gateway.mu.Unlock()
	if !found {
		return nil, status.Error(codes.Unauthenticated, "model capability unavailable")
	}
	result := &pb.ModelCallResponse{CostStatus: "unknown"}
	config := gateway.provider
	endpoint, err := providerEndpoint(config)
	if err != nil || gateway.budget == nil || config.Validate() != nil {
		result.ErrorCode = "model_prerequisites_missing"
		return result, nil
	}
	if !validID(request.CallId) || len(request.PayloadJson) > 8<<20 {
		return nil, status.Error(codes.InvalidArgument, "invalid model request")
	}
	purpose := request.Purpose
	if purpose == "" {
		purpose = "foreground"
	}
	if purpose != "foreground" && purpose != "compaction" && purpose != "memory_reflection" && purpose != "workflow_worker" {
		return nil, status.Error(codes.InvalidArgument, "unsupported model purpose")
	}
	var payload map[string]any
	if err := json.Unmarshal(request.PayloadJson, &payload); err != nil || payload == nil || payload["model"] != config.Model {
		return nil, status.Error(codes.InvalidArgument, "model payload does not match the admitted profile")
	}
	allowed := map[string]bool{"model": true, "messages": true, "system": true, "temperature": true, "tools": true,
		"tool_choice": true, "parallel_tool_calls": true, "stop": true, "max_tokens": true, "max_completion_tokens": true,
		"thinking": true, "reasoning_effort": true, "response_format": true, "stream": true, "stream_options": true, "n": true}
	for key := range payload {
		if !allowed[key] {
			return nil, status.Error(codes.InvalidArgument, "unsupported model payload field")
		}
	}
	if raw, exists := payload["tools"]; exists {
		tools, ok := raw.([]any)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "invalid model tools")
		}
		for _, item := range tools {
			tool, ok := item.(map[string]any)
			if !ok || (config.Protocol == "openai" && tool["type"] != "function") || (config.Protocol == "anthropic" && tool["type"] != nil) {
				return nil, status.Error(codes.InvalidArgument, "provider-hosted tools are not admitted")
			}
		}
	}
	payload["stream"] = false
	if config.Protocol == "openai" {
		// The completion limit includes reasoning output on reasoning models.
		payload["max_completion_tokens"] = config.OutputLimit
		delete(payload, "max_tokens")
	} else {
		payload["max_tokens"] = config.OutputLimit
		delete(payload, "max_completion_tokens")
	}
	if n, exists := payload["n"]; exists && n != float64(1) {
		return nil, status.Error(codes.InvalidArgument, "multiple model choices are not admitted")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid model payload")
	}
	invokeAttempt := func(callID, retryOf string) (*pb.ModelCallResponse, []string) {
		result := &pb.ModelCallResponse{CostStatus: "unknown"}
		if ctx.Err() != nil {
			result.ErrorCode = "provider_retry_canceled"
			return result, nil
		}
		gateway.mu.Lock()
		bound, active := gateway.scopes[digest]
		checkTask = gateway.taskAdmission
		gateway.mu.Unlock()
		if !active || bound != scope {
			result.ErrorCode = "model_gateway_unavailable"
			return result, nil
		}
		if checkTask != nil && checkTask(ctx, scope.taskID) != nil {
			result.ErrorCode = "task_prerequisites_missing"
			return result, nil
		}
		// A live attempt fences dispatch across the canonical Ledger; terminal unknown usage is audited.
		if _, err := gateway.budget.reserve(ctx, scope.owner, scope.taskID, callID, config.tokenLimit(), 1, purpose, retryOf, scope.id); err != nil {
			result.ErrorCode = admissionErrorCode(err)
			return result, nil
		}
		confirmed := false
		defer func() {
			if !confirmed {
				auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = gateway.budget.MarkUnknown(auditCtx, scope.owner, callID)
			}
		}()
		upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			result.ErrorCode = "provider_request_invalid"
			return result, nil
		}
		upstream.Header.Set("Content-Type", "application/json")
		if config.Protocol == "anthropic" {
			upstream.Header.Set("x-api-key", config.APIKey)
			upstream.Header.Set("anthropic-version", "2023-06-01")
		} else {
			upstream.Header.Set("Authorization", "Bearer "+config.APIKey)
		}
		response, err := gateway.http.Do(upstream)
		if err != nil {
			result.ErrorCode = "provider_transport_unknown"
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				result.ErrorCode = "provider_timeout_unknown"
			}
			return result, nil
		}
		defer response.Body.Close()
		result.HttpStatus = int32(response.StatusCode)
		if response.StatusCode < 100 || response.StatusCode > 599 {
			result.ErrorCode = "provider_http_unknown"
			return result, nil
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
		if err != nil || len(data) > 16<<20 {
			result.ErrorCode = "provider_response_unknown"
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				result.ErrorCode = "provider_timeout_unknown"
			}
			return result, nil
		}
		failure := "provider_http"
		if response.StatusCode == http.StatusTooManyRequests {
			failure = "provider_rate_limited"
		} else if response.StatusCode >= 500 && response.StatusCode <= 599 {
			failure = "provider_transient"
		} else if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			failure = "provider_authentication"
		}
		usage, err := normalizeUsage(config.Protocol, data)
		if err != nil {
			result.ErrorCode = "provider_usage_unknown"
			if response.StatusCode != http.StatusOK {
				result.ErrorCode = failure + "_unknown"
			}
			return result, nil
		}
		if usage.input > config.InputLimit || (config.TotalLimit == 0 && usage.output > config.OutputLimit) || usage.input+usage.output > config.tokenLimit() {
			result.ErrorCode = "provider_bound_violated"
			auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = gateway.budget.markUnknown(auditCtx, scope.owner, callID, usage.input+usage.output, true)
			return result, nil
		}
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := gateway.budget.settle(auditCtx, scope.owner, callID, usage.input+usage.output,
			budgetFact{InputTokens: usage.input, OutputTokens: usage.output, CachedTokens: usage.cached, CostStatus: "unknown", HTTPStatus: result.HttpStatus}); err != nil {
			result.ErrorCode = "provider_settlement_unknown"
			return result, nil
		}
		confirmed = true
		result.InputTokens, result.OutputTokens, result.CachedInputTokens = usage.input, usage.output, usage.cached
		if response.StatusCode != http.StatusOK {
			result.ErrorCode = failure
			return result, response.Header.Values("Retry-After")
		}
		// Canonical JSON exposes escaped echoes before they cross into Python.
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if decoder.Decode(&decoded) != nil {
			result.ErrorCode = "provider_response_unknown"
			return result, nil
		}
		canonical, err := json.Marshal(decoded)
		if err != nil {
			result.ErrorCode = "provider_response_unknown"
			return result, nil
		}
		encodedKey, _ := json.Marshal(config.APIKey)
		result.PayloadJson = bytes.ReplaceAll(canonical, encodedKey[1:len(encodedKey)-1], []byte("<redacted>"))
		return result, nil
	}
	callID, retryOf := request.CallId, ""
	var total tokenUsage
	for attempt := 0; attempt < modelAttemptLimit; attempt++ {
		current, retryAfter := invokeAttempt(callID, retryOf)
		total.input += current.InputTokens
		total.output += current.OutputTokens
		total.cached += current.CachedInputTokens
		current.InputTokens, current.OutputTokens, current.CachedInputTokens = total.input, total.output, total.cached
		if attempt == modelAttemptLimit-1 || (current.ErrorCode != "provider_rate_limited" && current.ErrorCode != "provider_transient") {
			return current, nil
		}
		delay, allowed := retryAfterDelay(retryAfter, time.Now())
		if !allowed {
			return current, nil
		}
		if backoff := time.Second << attempt; delay < backoff {
			delay = backoff
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			current.ErrorCode = "provider_retry_canceled"
			return current, nil
		case <-timer.C:
		}
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			current.ErrorCode = "token_admission_unavailable"
			return current, nil
		}
		retryOf, callID = callID, hex.EncodeToString(nonce)
	}
	result.ErrorCode = "token_admission_unavailable"
	return result, nil
}

func retryAfterDelay(values []string, now time.Time) (time.Duration, bool) {
	// Retry-After is a single value; joining field lines can synthesize an HTTP-date.
	if len(values) > 1 {
		return 0, false
	}
	if len(values) == 0 {
		return 0, true
	}
	value := values[0]
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		if seconds > 10 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	date, err := http.ParseTime(value)
	if err != nil || date.Sub(now) > 10*time.Second {
		return 0, false
	}
	return max(0, date.Sub(now)), true
}

func (config ProviderConfig) Validate() error {
	if _, err := providerEndpoint(config); err != nil {
		return err
	}
	if config.InputLimit <= 0 || config.OutputLimit <= 0 || config.InputLimit > VerificationTokenLimit-config.OutputLimit {
		return ErrInvalidAdmission
	}
	if config.TotalLimit < 0 || config.TotalLimit > VerificationTokenLimit || (config.TotalLimit > 0 && config.TotalLimit < config.InputLimit+config.OutputLimit) {
		return ErrInvalidAdmission
	}
	return nil
}

func (config ProviderConfig) tokenLimit() int64 {
	if config.TotalLimit > 0 {
		return config.TotalLimit
	}
	return config.InputLimit + config.OutputLimit
}

func providerEndpoint(config ProviderConfig) (string, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || config.APIKey == "" || config.Model == "" {
		return "", ErrInvalidAdmission
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return "", ErrInvalidAdmission
		}
	}
	base := strings.TrimRight(config.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	switch config.Protocol {
	case "openai":
		return base + "/chat/completions", nil
	case "anthropic":
		return base + "/messages", nil
	default:
		return "", ErrInvalidAdmission
	}
}

type tokenUsage struct{ input, output, cached int64 }

func accountingJSONFields(data []byte, uniqueNames ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, ErrUsageUnknown
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, ErrUsageUnknown
		}
		if _, duplicate := fields[name]; duplicate && slices.Contains(uniqueNames, name) {
			return nil, ErrUsageUnknown
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, ErrUsageUnknown
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, ErrUsageUnknown
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrUsageUnknown
	}
	return fields, nil
}

func normalizeUsage(protocol string, data []byte) (tokenUsage, error) {
	// Duplicate accounting fields cannot establish usage, even when a later value is smaller.
	response, err := accountingJSONFields(data, "usage")
	if err != nil {
		return tokenUsage{}, ErrUsageUnknown
	}
	uniqueNames := []string{"prompt_tokens", "completion_tokens", "prompt_tokens_details"}
	if protocol == "anthropic" {
		uniqueNames = []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"}
	}
	usage, err := accountingJSONFields(response["usage"], uniqueNames...)
	if err != nil {
		return tokenUsage{}, ErrUsageUnknown
	}
	read := func(name string) (int64, error) {
		raw, found := usage[name]
		if !found || bytes.Equal(raw, []byte("null")) {
			return 0, ErrUsageUnknown
		}
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil || value < 0 || value > VerificationTokenLimit {
			return 0, ErrUsageUnknown
		}
		return value, nil
	}
	if protocol == "anthropic" {
		input, inErr := read("input_tokens")
		output, outErr := read("output_tokens")
		if inErr != nil || outErr != nil {
			return tokenUsage{}, ErrUsageUnknown
		}
		cached, creation := int64(0), int64(0)
		var err error
		if _, ok := usage["cache_read_input_tokens"]; ok {
			cached, err = read("cache_read_input_tokens")
			if err != nil {
				return tokenUsage{}, err
			}
		}
		if _, ok := usage["cache_creation_input_tokens"]; ok {
			creation, err = read("cache_creation_input_tokens")
			if err != nil {
				return tokenUsage{}, err
			}
		}
		if input > VerificationTokenLimit-cached || input+cached > VerificationTokenLimit-creation {
			return tokenUsage{}, ErrUsageUnknown
		}
		return tokenUsage{input: input + cached + creation, output: output, cached: cached}, nil
	}
	if protocol != "openai" {
		return tokenUsage{}, ErrUsageUnknown
	}
	input, inErr := read("prompt_tokens")
	output, outErr := read("completion_tokens")
	if inErr != nil || outErr != nil {
		return tokenUsage{}, ErrUsageUnknown
	}
	var cached int64
	if raw, ok := usage["prompt_tokens_details"]; ok && !bytes.Equal(raw, []byte("null")) {
		details, err := accountingJSONFields(raw, "cached_tokens")
		if err != nil {
			return tokenUsage{}, ErrUsageUnknown
		}
		if raw, ok := details["cached_tokens"]; ok && json.Unmarshal(raw, &cached) != nil {
			return tokenUsage{}, ErrUsageUnknown
		}
	}
	if cached < 0 || cached > input {
		return tokenUsage{}, ErrUsageUnknown
	}
	return tokenUsage{input: input, output: output, cached: cached}, nil
}

func admissionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrBudgetExhausted):
		return "token_budget_exhausted"
	case errors.Is(err, ErrUsageUnknown):
		return "token_usage_unknown"
	case errors.Is(err, ErrTaskBusy):
		return "code_task_busy"
	case errors.Is(err, ErrRelayBusy):
		return "model_concurrency_exhausted"
	case errors.Is(err, ErrCallReserved):
		return "model_call_already_reserved"
	case errors.Is(err, ErrTaskClosed):
		return "code_task_closed"
	default:
		return "token_admission_unavailable"
	}
}
