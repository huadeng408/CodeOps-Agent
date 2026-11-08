package admission

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
)

type tokenUsage struct {
	input, output, cached int64
	visibleOutput         *int64
}

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

func normalizeUsage(protocol, model string, data []byte) (tokenUsage, error) {
	// Duplicate accounting fields cannot establish usage, even when a later value is smaller.
	response, err := accountingJSONFields(data, "usage")
	if err != nil {
		return tokenUsage{}, ErrUsageUnknown
	}
	uniqueNames := []string{"prompt_tokens", "completion_tokens", "total_tokens", "prompt_tokens_details", "completion_tokens_details"}
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
	var reasoning int64
	reasoningKnown := false
	if raw, ok := usage["completion_tokens_details"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		details, err := accountingJSONFields(raw, "reasoning_tokens")
		if err != nil {
			return tokenUsage{}, ErrUsageUnknown
		}
		if raw, ok := details["reasoning_tokens"]; ok {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &reasoning) != nil || reasoning < 0 || reasoning > VerificationTokenLimit {
				return tokenUsage{}, ErrUsageUnknown
			}
			reasoningKnown = true
		}
	}
	if _, ok := usage["total_tokens"]; ok {
		total, err := read("total_tokens")
		if err != nil {
			return tokenUsage{}, ErrUsageUnknown
		}
		if total != input+output {
			// Grok can report visible output separately; the total must prove the extra reasoning.
			if !strings.HasPrefix(model, "grok-") || reasoning == 0 || total != input+output+reasoning {
				return tokenUsage{}, ErrUsageUnknown
			}
			output += reasoning
		}
	} else if strings.HasPrefix(model, "grok-") && reasoning > 0 {
		return tokenUsage{}, ErrUsageUnknown
	}
	if reasoning > output {
		return tokenUsage{}, ErrUsageUnknown
	}
	var visible *int64
	if reasoningKnown {
		var choices []struct {
			Message struct {
				Content      *string
				ToolCalls    []json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage   `json:"function_call"`
			}
		}
		if json.Unmarshal(response["choices"], &choices) == nil && len(choices) == 1 {
			message := choices[0].Message
			// Function tokens are not subject to the visible-answer cap either.
			if message.Content != nil && len(message.ToolCalls) == 0 && (len(message.FunctionCall) == 0 || bytes.Equal(bytes.TrimSpace(message.FunctionCall), []byte("null"))) {
				value := output - reasoning
				visible = &value
			}
		}
	}
	return tokenUsage{input: input, output: output, cached: cached, visibleOutput: visible}, nil
}
