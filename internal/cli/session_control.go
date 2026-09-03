package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"code-agent/internal/tools"
)

const sessionControlAPIVersion = "v1"

type sessionControlReceipt struct {
	APIVersion      string `json:"api_version"`
	Operation       string `json:"operation"`
	SessionID       string `json:"session_id"`
	SourceSessionID string `json:"source_session_id,omitempty"`
	TargetSessionID string `json:"target_session_id,omitempty"`
	TargetSeq       int64  `json:"target_seq"`
	MessageCount    int    `json:"message_count"`
}

func (a *App) executeSessionControl(ctx context.Context, name string, params map[string]any) tools.ToolResult {
	result := tools.ToolResult{Name: name}
	if a.session == nil {
		return sessionControlError(result, "session manager is unavailable")
	}
	if err := requireSessionControlVersion(params); err != nil {
		return sessionControlError(result, err.Error())
	}
	operation, ok := params["operation"].(string)
	expected := map[string]string{"SessionFork": "fork", "SessionRewind": "rewind"}[name]
	if !ok || strings.TrimSpace(operation) != expected {
		return sessionControlError(result, fmt.Sprintf("operation must be %q", expected))
	}

	source := a.session.Current()
	switch name {
	case "SessionFork":
		targetID, err := requiredSessionString(params, "target_session_id")
		if err != nil {
			return sessionControlError(result, err.Error())
		}
		targetSeq, err := requiredSessionSeq(params, "target_seq")
		if err != nil {
			return sessionControlError(result, err.Error())
		}
		forked, err := a.session.Fork(ctx, targetID, targetSeq)
		if err != nil {
			return sessionControlError(result, "session fork failed: "+err.Error())
		}
		result.Output = marshalSessionControlReceipt(sessionControlReceipt{
			APIVersion:      sessionControlAPIVersion,
			Operation:       "fork",
			SessionID:       forked.ID,
			SourceSessionID: source.ID,
			TargetSessionID: forked.ID,
			TargetSeq:       targetSeq,
			MessageCount:    len(forked.Messages),
		})
		return result

	case "SessionRewind":
		targetSeq, err := requiredSessionSeq(params, "target_seq")
		if err != nil {
			return sessionControlError(result, err.Error())
		}
		rewound, err := a.session.Rewind(ctx, targetSeq)
		if err != nil {
			return sessionControlError(result, "session rewind failed: "+err.Error())
		}
		result.Output = marshalSessionControlReceipt(sessionControlReceipt{
			APIVersion:   sessionControlAPIVersion,
			Operation:    "rewind",
			SessionID:    rewound.ID,
			TargetSeq:    targetSeq,
			MessageCount: len(rewound.Messages),
		})
		return result
	default:
		return sessionControlError(result, "unsupported session control tool: "+name)
	}
}

func requireSessionControlVersion(params map[string]any) error {
	version, ok := params["api_version"].(string)
	if !ok || strings.TrimSpace(version) == "" {
		return fmt.Errorf("api_version must be %q", sessionControlAPIVersion)
	}
	if strings.TrimSpace(version) != sessionControlAPIVersion {
		return fmt.Errorf("unsupported session control api_version: %q", version)
	}
	return nil
}

func requiredSessionString(params map[string]any, key string) (string, error) {
	value, ok := params[key].(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return value, nil
}

func requiredSessionSeq(params map[string]any, key string) (int64, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	switch number := value.(type) {
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number != math.Trunc(number) || number >= float64(math.MaxInt64) {
			return 0, fmt.Errorf("%s must be a non-negative integer", key)
		}
		return int64(number), nil
	case int:
		if number < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer", key)
		}
		return int64(number), nil
	case int64:
		if number < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer", key)
		}
		return number, nil
	case json.Number:
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
}

func sessionControlError(result tools.ToolResult, message string) tools.ToolResult {
	result.Error = message
	result.ExitCode = 1
	return result
}

func marshalSessionControlReceipt(receipt sessionControlReceipt) string {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return "{\"error\":\"failed to encode session control receipt\"}"
	}
	return string(payload)
}
