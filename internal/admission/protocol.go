package admission

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Protocol adapters never reserve, settle, or retry a model call.
type providerProtocol interface {
	prepare(map[string]any) error
	authenticate(http.Header)
	read(*http.Response) ([]byte, error)
	usage([]byte) (tokenUsage, error)
}

type openAIProtocol struct{ config ProviderConfig }
type anthropicProtocol struct{ config ProviderConfig }

func protocolFor(config ProviderConfig) providerProtocol {
	if config.Protocol == "anthropic" {
		return anthropicProtocol{config}
	}
	return openAIProtocol{config}
}

func providerTools(payload map[string]any) ([]map[string]any, error) {
	raw, exists := payload["tools"]
	if !exists {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "invalid model tools")
	}
	tools := make([]map[string]any, 0, len(items))
	for _, item := range items {
		tool, ok := item.(map[string]any)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "invalid model tools")
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func (adapter openAIProtocol) prepare(payload map[string]any) error {
	tools, err := providerTools(payload)
	if err != nil {
		return err
	}
	for _, tool := range tools {
		if tool["type"] != "function" {
			return status.Error(codes.InvalidArgument, "provider-hosted tools are not admitted")
		}
	}
	payload["stream"] = adapter.config.ResponseMode == "sse"
	if adapter.config.ResponseMode == "sse" {
		payload["stream_options"] = map[string]any{"include_usage": true}
	} else {
		delete(payload, "stream_options")
	}
	if strings.HasPrefix(strings.ToLower(adapter.config.Model), "deepseek-") {
		payload["max_tokens"] = adapter.config.OutputLimit
		delete(payload, "max_completion_tokens")
	} else {
		payload["max_completion_tokens"] = adapter.config.OutputLimit
		delete(payload, "max_tokens")
	}
	return adapter.config.applyRequestProfile(payload)
}

func (adapter anthropicProtocol) prepare(payload map[string]any) error {
	tools, err := providerTools(payload)
	if err != nil {
		return err
	}
	for _, tool := range tools {
		if _, exists := tool["type"]; exists {
			return status.Error(codes.InvalidArgument, "provider-hosted tools are not admitted")
		}
	}
	payload["stream"] = false
	delete(payload, "stream_options")
	payload["max_tokens"] = adapter.config.OutputLimit
	delete(payload, "max_completion_tokens")
	return adapter.config.applyRequestProfile(payload)
}

func (adapter openAIProtocol) authenticate(headers http.Header) {
	headers.Set("Authorization", "Bearer "+adapter.config.APIKey)
	if adapter.config.ResponseMode == "sse" {
		headers.Set("Accept", "text/event-stream")
	}
}

func (adapter anthropicProtocol) authenticate(headers http.Header) {
	headers.Set("x-api-key", adapter.config.APIKey)
	headers.Set("anthropic-version", "2023-06-01")
}

func (adapter openAIProtocol) read(response *http.Response) ([]byte, error) {
	if adapter.config.ResponseMode == "sse" && response.StatusCode == http.StatusOK {
		data, err := readOpenAIStream(response, adapter.config.Model)
		if err != nil {
			reason := "stream_read_unknown"
			var protocolError streamProtocolError
			var providerError *streamProviderError
			if errors.As(err, &providerError) {
				reason = "provider_stream_error"
				metadata, _ := json.Marshal(map[string]any{"error": providerError.metadata})
				logProviderFailure(response.StatusCode, metadata, adapter.config.APIKey)
			} else if errors.As(err, &protocolError) {
				reason = string(protocolError)
			} else if errors.Is(err, io.ErrUnexpectedEOF) {
				reason = "incomplete_stream"
			}
			if adapter.config.APIKey != "" && strings.Contains(reason, adapter.config.APIKey) {
				reason = "unknown"
			}
			log.Printf("model stream failure: http_status=%d reason=%s", response.StatusCode, reason)
		}
		return data, err
	}
	return readProviderJSON(response)
}

func (adapter anthropicProtocol) read(response *http.Response) ([]byte, error) {
	return readProviderJSON(response)
}

func readProviderJSON(response *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
}

func (adapter openAIProtocol) usage(data []byte) (tokenUsage, error) {
	return normalizeUsage("openai", adapter.config.Model, data)
}

func (adapter anthropicProtocol) usage(data []byte) (tokenUsage, error) {
	return normalizeUsage("anthropic", adapter.config.Model, data)
}

func logProviderFailure(status int, data []byte, key string) {
	var envelope struct {
		Error struct{ Type, Code, Param json.RawMessage }
	}
	if json.Unmarshal(data, &envelope) != nil {
		envelope.Error.Type, envelope.Error.Code, envelope.Error.Param = nil, nil, nil
	}
	// Only fixed labels survive; provider messages can echo credentials.
	safe := func(raw json.RawMessage) string {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return "unknown"
		}
		if key != "" && strings.Contains(value, key) {
			return "unknown"
		}
		switch value {
		case "server_error", "internal_error", "api_error", "new_api_error", "upstream_error", "invalid_request_error",
			"authentication_error", "permission_error", "not_found_error", "overloaded_error", "rate_limit_error",
			"rate_limit_exceeded", "insufficient_quota", "insufficient_user_quota", "insufficient_token_quota",
			"no_available_channel", "model_not_found", "unsupported_parameter", "invalid_parameter", "invalid_api_key",
			"context_length_exceeded", "bad_response_status_code", "timeout",
			"model", "messages", "temperature", "max_tokens", "max_completion_tokens", "reasoning_effort",
			"tools", "tool_choice", "parallel_tool_calls", "response_format", "stream":
			return value
		}
		return "unknown"
	}
	log.Printf("model provider failure: http_status=%d type=%s code=%s param=%s", status,
		safe(envelope.Error.Type), safe(envelope.Error.Code), safe(envelope.Error.Param))
}
