package tools

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"

	"code-agent/internal/skills"
)

func executeSkillResource(manager *skills.Manager, name string, args map[string]any) (ToolResult, error) {
	resource, ok := args["resource"].(string)
	if !ok || strings.TrimSpace(resource) == "" {
		return ToolResult{Name: "Skill", Error: "resource must be a non-empty relative file path", ExitCode: 1}, nil
	}
	data, err := manager.ReadResourceForModel(name, resource)
	if err != nil {
		return ToolResult{Name: "Skill", Error: err.Error(), ExitCode: 1}, nil
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return ToolResult{Name: "Skill", Error: "skill resource must be UTF-8 text", ExitCode: 1}, nil
	}
	return ToolResult{Name: "Skill", Output: fmt.Sprintf("Skill resource: %s/%s\nSHA-256: %x\n\n%s", name, resource, sha256.Sum256(data), data)}, nil
}
