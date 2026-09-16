package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/mcp"
	"code-agent/internal/skills"
)

func TestSkillToolReadsConfinedResourceWithProvenance(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "skills", "workflow")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte("---\nname: workflow\ndescription: Workflow\n---\nUse references/guide.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "guide.txt"), []byte("workflow guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := skills.NewManager()
	if err := manager.Discover(skills.DiscoveryOptions{ProjectDir: filepath.Dir(base)}); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(root)
	defer executor.Close()
	executor.SetSkillsManager(manager)
	result, err := executor.Execute(context.Background(), ToolRequest{Name: "Skill", Arguments: map[string]any{"name": "workflow", "resource": "guide.txt"}})
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "workflow guide") || !strings.Contains(result.Output, "SHA-256:") {
		t.Fatalf("resource result = %+v, %v", result, err)
	}
	for _, resource := range []any{"../outside.txt", "", 1, "SKILL.md/.."} {
		result, err := executor.Execute(context.Background(), ToolRequest{Name: "Skill", Arguments: map[string]any{"name": "workflow", "resource": resource}})
		if err != nil || result.ExitCode == 0 || result.Output != "" {
			t.Fatalf("invalid resource %v was not refused: %+v, %v", resource, result, err)
		}
	}
	manager.Register(skills.Skill{Name: "workflow", Description: "Private", ResourceBase: base, Invocation: skills.InvocationPolicy{Configured: true}})
	result, _ = executor.Execute(context.Background(), ToolRequest{Name: "Skill", Arguments: map[string]any{"name": "workflow", "resource": "guide.txt"}})
	if result.ExitCode == 0 || !strings.Contains(result.Error, "not model-invocable") {
		t.Fatalf("resource bypassed invocation policy: %+v", result)
	}
}

func TestSkillAndMCPSpansDoNotRecordArgumentsOrResults(t *testing.T) {
	executor := NewExecutor(t.TempDir())
	t.Cleanup(func() { _ = executor.Close() })
	tracer := &recordingTracer{}
	executor.SetTracer(tracer)

	skillManager := skills.NewManager()
	skillManager.Register(skills.Skill{Name: "trace-safe", Description: "Trace test", Prompt: "SKILL_RESULT_SECRET_SENTINEL"})
	executor.SetSkillsManager(skillManager)
	result, err := executor.Execute(context.Background(), ToolRequest{Name: "Skill", Arguments: map[string]any{
		"name": "trace-safe", "args": "SKILL_ARGUMENT_SECRET_SENTINEL",
	}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("skill result = %+v, err=%v", result, err)
	}

	mcpManager := mcp.NewManager()
	mcpManager.RegisterTool(mcp.ToolDefinition{Name: "fixture__inspect", Server: "offline"})
	executor.SetMCPManager(mcpManager)
	_, _ = executor.Execute(context.Background(), ToolRequest{Name: "fixture__inspect", Arguments: map[string]any{
		"token": "MCP_ARGUMENT_SECRET_SENTINEL",
	}})

	if len(tracer.spans) != 2 || tracer.spans[0].name != "skill.load" || tracer.spans[1].name != "tool.mcp" {
		t.Fatalf("spans = %+v", tracer.spans)
	}
	for _, span := range tracer.spans {
		if !span.ended {
			t.Fatalf("span %s was not ended", span.name)
		}
		for _, attr := range span.attrs {
			if strings.Contains(attr.Value.Emit(), "SECRET_SENTINEL") {
				t.Fatalf("span %s leaked payload in %s", span.name, attr.Key)
			}
		}
		for _, event := range span.events {
			if strings.Contains(event, "SECRET_SENTINEL") {
				t.Fatalf("span %s leaked payload in event", span.name)
			}
		}
	}
}
