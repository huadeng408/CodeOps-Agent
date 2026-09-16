package tools

import (
	"context"
	"strings"
	"testing"

	"code-agent/internal/mcp"
)

func TestMCPAndSkillFailureSpansUseContentFreeMarker(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		prepare  func(*testing.T, *Executor)
		wantSpan string
	}{
		{
			name:     "skill",
			tool:     "Skill",
			wantSpan: "skill.load",
		},
		{
			name: "mcp",
			tool: "fixture__offline",
			prepare: func(t *testing.T, executor *Executor) {
				manager := mcp.NewManager()
				if err := manager.RegisterTool(mcp.ToolDefinition{Name: "fixture__offline", Server: "offline"}); err != nil {
					t.Fatal(err)
				}
				executor.SetMCPManager(manager)
			},
			wantSpan: "tool.mcp",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := NewExecutor(t.TempDir())
			t.Cleanup(func() { _ = executor.Close() })
			tracer := &recordingTracer{}
			executor.SetTracer(tracer)
			if test.prepare != nil {
				test.prepare(t, executor)
			}

			result, _ := executor.Execute(context.Background(), ToolRequest{
				Name: test.tool, Arguments: map[string]any{"secret": "FAILURE_SECRET_SENTINEL"},
			})
			if result.ExitCode == 0 || result.Error == "" {
				t.Fatalf("failure unexpectedly succeeded: %+v", result)
			}
			if len(tracer.spans) != 1 || tracer.spans[0].name != test.wantSpan || !tracer.spans[0].ended {
				t.Fatalf("spans = %+v, want one ended %s", tracer.spans, test.wantSpan)
			}
			span := tracer.spans[0]
			if len(span.errors) != 1 || span.errors[0].Error() != "operation failed" {
				t.Fatalf("failure marker = %v", span.errors)
			}
			for _, attr := range span.attrs {
				if strings.Contains(attr.Value.Emit(), "SECRET_SENTINEL") {
					t.Fatalf("span leaked arguments in %s", attr.Key)
				}
			}
		})
	}
}
