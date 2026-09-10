package session

import (
	"encoding/json"
	"testing"
)

func TestConversationHistoryCarriesCanonicalEventIdentity(t *testing.T) {
	payloads := []struct {
		kind    string
		payload any
	}{
		{userMessageEventType, messagePayload{Author: "user", Content: "fact"}},
		{"tool/call", toolCallPayload{RunID: "run", ToolCallID: "call", ToolName: "Read", ArgumentsJSON: `{}`}},
		{"tool/result", toolResultPayload{RunID: "run", ToolCallID: "call", ToolName: "Read", Output: "result"}},
	}
	var events []Event
	for i, item := range payloads {
		payload, err := json.Marshal(item.payload)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, Event{EventID: item.kind, Seq: int64(i), Type: item.kind, Payload: payload, Checksum: "hash-" + item.kind})
	}
	history, _, err := conversationHistory(events, events, "run", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(events) {
		t.Fatalf("history count=%d", len(history))
	}
	for i, message := range history {
		if message.EventID != events[i].EventID || message.EventChecksum != events[i].Checksum {
			t.Fatalf("lost identity at %d", i)
		}
	}
}
