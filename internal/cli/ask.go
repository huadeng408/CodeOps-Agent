package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
)

func (a *App) handleAskUserRequest(ctx context.Context, request *codeagentpb.AskUserRequest) (orchestrator.ToolResult, error) {
	if request == nil {
		return orchestrator.ToolResult{}, errors.New("ask user request is nil")
	}
	if a.input == nil {
		return orchestrator.ToolResult{}, errors.New("input is not available")
	}

	options := request.GetOptions()
	viewOptions := make([]QuestionOption, 0, len(options))
	for idx, option := range options {
		if option == nil {
			continue
		}
		label := strings.TrimSpace(option.GetLabel())
		if label == "" {
			continue
		}
		_ = idx
		viewOptions = append(viewOptions, QuestionOption{Label: label, Description: strings.TrimSpace(option.GetDescription()), Preview: truncateForMetadata(strings.TrimSpace(option.GetPreview()), 120)})
	}
	if a.renderer != nil {
		a.renderer.PrintQuestion(QuestionView{Question: strings.TrimSpace(request.GetQuestion()), Options: viewOptions, MultiSelect: request.GetMultiSelect()})
	}

	answer, err := a.input.ReadLine(ctx)
	if err != nil {
		return orchestrator.ToolResult{}, err
	}

	output, cancelled := normalizeAskUserAnswer(answer, options, request.GetMultiSelect())
	result := orchestrator.ToolResult{
		ToolCallID: request.GetAskUserId(),
		ToolName:   "AskUser",
		Output:     output,
		ExitCode:   0,
	}
	if cancelled {
		result.Output = ""
		result.Error = "user cancelled"
		result.ExitCode = 1
	}
	return result, nil
}

func normalizeAskUserAnswer(answer string, options []*codeagentpb.Option, multiSelect bool) (string, bool) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		if len(options) > 0 {
			return "", true
		}
		return "", false
	}
	if isAskUserCancelAnswer(trimmed) {
		return "", true
	}

	if multiSelect {
		tokens := splitAskUserTokens(trimmed)
		selected := make([]string, 0, len(tokens))
		seen := map[string]struct{}{}
		for _, token := range tokens {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			value := resolveAskUserOption(token, options)
			if value == "" {
				value = token
			}
			key := strings.ToLower(value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			selected = append(selected, value)
		}
		if len(selected) == 0 {
			return "", true
		}
		if data, err := json.Marshal(selected); err == nil {
			return string(data), false
		}
		return strings.Join(selected, ", "), false
	}

	if value := resolveAskUserOption(trimmed, options); value != "" {
		return value, false
	}
	return trimmed, false
}

func resolveAskUserOption(token string, options []*codeagentpb.Option) string {
	if len(options) == 0 {
		return ""
	}
	if index, err := strconv.Atoi(token); err == nil && index > 0 && index <= len(options) {
		if option := options[index-1]; option != nil {
			return strings.TrimSpace(option.GetLabel())
		}
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		label := strings.TrimSpace(option.GetLabel())
		if label != "" && strings.EqualFold(token, label) {
			return label
		}
	}
	return ""
}

func splitAskUserTokens(answer string) []string {
	return strings.FieldsFunc(answer, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func isAskUserCancelAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "cancel", "abort", "quit", "exit":
		return true
	default:
		return false
	}
}
