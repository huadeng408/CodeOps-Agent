package session

import (
	"encoding/json"
	"testing"
)

func TestToolEventViewExposesExecutionReceipt(t *testing.T) {
	for _, kind := range []string{"tool/call", "tool/dispatched", "tool/result"} {
		payload := map[string]any{"tool_name": "Read", "arguments_json": "{}", "output": "repository evidence", "exit_code": 0}
		raw, _ := json.Marshal(payload)
		view, err := eventToView(Event{Type: kind, Payload: raw})
		if err != nil || view.ToolName != "Read" || view.Content == "" {
			t.Fatalf("missing tool view: %#v, %v", view, err)
		}
		if kind == "tool/result" && view.ToolOutput != "repository evidence" {
			t.Fatalf("missing tool output: %#v", view)
		}
	}
}
