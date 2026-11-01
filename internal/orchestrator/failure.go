package orchestrator

import (
	"errors"
	"strings"
	"sync"

	"google.golang.org/grpc/status"
)

// FailureDetails returns fixed categories only. RPC messages can contain secrets.
func FailureDetails(cause error) map[string]string {
	stage := "conversation"
	if cause == nil {
		stage = "unknown"
	} else {
		message := cause.Error()
		switch {
		case strings.HasPrefix(message, "receive orchestrator message:"):
			stage = "orchestrator_receive"
		case strings.HasPrefix(message, "send user input:"):
			stage = "orchestrator_send"
		case strings.HasPrefix(message, "open conversation stream:"):
			stage = "orchestrator_stream_open"
		case strings.HasPrefix(message, "conversation model admission unavailable"):
			stage = "model_scope_admission"
		case strings.HasPrefix(message, "bind actor to session:"):
			stage = "actor_binding"
		case errors.Is(cause, ErrCompactionPersistence):
			stage = "compaction_persistence"
		case errors.Is(cause, ErrConversationFailed):
			stage = "orchestrator_terminal"
		}
	}
	details := map[string]string{"stage": stage, "grpc_status": status.Code(cause).String()}
	var startup *startupFailure
	if errors.As(cause, &startup) {
		details["startup_reason"] = startup.reason
		details["child_stage"] = startup.childStage
	}
	if code := ConversationFailureCode(cause); code != "" {
		details["terminal_code"] = code
		if rpc := gatewayRPCStatus(code); rpc != "" {
			details["gateway_grpc_status"] = rpc
		}
	}
	return details
}

type conversationFailure struct{ code string }

func (e *conversationFailure) Error() string { return "orchestrator conversation failed: " + e.code }
func (e *conversationFailure) Unwrap() error { return ErrConversationFailed }

func ConversationFailureCode(cause error) string {
	var failure *conversationFailure
	if errors.As(cause, &failure) {
		return failure.code
	}
	return ""
}

func failedConversation(code string) error {
	if gatewayRPCStatus(code) != "" {
		return &conversationFailure{code: code}
	}
	switch code {
	case "model_prerequisites_missing", "task_prerequisites_missing", "model_gateway_binding_invalid", "model_gateway_unavailable",
		"model_gateway_response_invalid", "provider_request_invalid", "provider_transport_unknown", "provider_timeout_unknown",
		"provider_response_unknown", "provider_http_unknown", "provider_rate_limited_unknown", "provider_transient_unknown",
		"provider_authentication_unknown", "provider_usage_unknown", "provider_http", "provider_rate_limited", "provider_transient",
		"provider_authentication", "provider_retry_canceled", "provider_bound_violated", "provider_settlement_unknown",
		"token_budget_exhausted", "wall_clock_budget_exhausted", "token_usage_unknown", "code_task_busy", "model_concurrency_exhausted", "model_call_already_reserved",
		"code_task_closed", "token_admission_unavailable", "provider_model_identity_unconfirmed", "rpc_cancelled", "rpc_unavailable",
		"rpc_deadline_exceeded", "provider_timeout", "provider_authentication_error", "provider_runtime_error", "tool_round_limit", "empty_model_response":
	default:
		code = "orchestrator_terminal_unknown"
	}
	return &conversationFailure{code: code}
}

func gatewayRPCStatus(code string) string {
	switch code {
	case "model_payload_mismatch", "unsupported_payload_field", "model_tools_invalid", "model_hosted_tools_rejected", "model_multiple_choices_rejected", "model_purpose_invalid", "model_request_invalid", "model_payload_invalid", "model_gateway_rpc_invalid_argument", "thinking_budget_invalid", "thinking_budget_exceeds_output_limit":
		return "InvalidArgument"
	case "model_gateway_rpc_cancelled":
		return "Canceled"
	case "model_gateway_rpc_unknown":
		return "Unknown"
	case "model_gateway_rpc_deadline_exceeded":
		return "DeadlineExceeded"
	case "model_gateway_rpc_not_found":
		return "NotFound"
	case "model_gateway_rpc_already_exists":
		return "AlreadyExists"
	case "model_gateway_rpc_permission_denied":
		return "PermissionDenied"
	case "model_gateway_rpc_resource_exhausted":
		return "ResourceExhausted"
	case "model_gateway_rpc_failed_precondition":
		return "FailedPrecondition"
	case "model_gateway_rpc_aborted":
		return "Aborted"
	case "model_gateway_rpc_out_of_range":
		return "OutOfRange"
	case "model_gateway_rpc_unimplemented":
		return "Unimplemented"
	case "model_gateway_rpc_internal":
		return "Internal"
	case "model_gateway_rpc_unavailable":
		return "Unavailable"
	case "model_gateway_rpc_data_loss":
		return "DataLoss"
	case "model_gateway_rpc_unauthenticated":
		return "Unauthenticated"
	}
	return ""
}

type startupFailure struct {
	reason, childStage string
	cause              error
}

func (e *startupFailure) Error() string { return "orchestrator startup failed" }
func (e *startupFailure) Unwrap() error { return e.cause }

// startupStages discards all child output except this fixed milestone allowlist.
type startupStages struct {
	mu         sync.Mutex
	tail, last string
}

func (s *startupStages) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := strings.ReplaceAll(s.tail+string(data), "\r\n", "\n")
	for _, stage := range []string{"python_entered", "imports_ready", "config_parsed", "telemetry_ready", "graph_ready", "context_ready", "grpc_ready"} {
		if strings.Contains(text, "CODE_AGENT_STARTUP:"+stage+"\n") {
			s.last = stage
		}
	}
	if len(text) > 80 {
		text = text[len(text)-80:]
	}
	s.tail = text
	return len(data), nil
}
func (s *startupStages) value() string { s.mu.Lock(); defer s.mu.Unlock(); return s.last }
func (s *startupStages) reset()        { s.mu.Lock(); defer s.mu.Unlock(); s.last, s.tail = "", "" }
