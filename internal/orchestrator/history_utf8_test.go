package orchestrator

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHistoryTruncationPreservesUTF8(t *testing.T) {
	content := strings.Repeat("\u4e2d", 2000)
	if !utf8.ValidString(truncateHistoryContent(content)) {
		t.Fatal("history truncation produced invalid UTF-8")
	}
}

func TestDurableConversationHistoryRetainsEarlyFactsAndLongMessages(t *testing.T) {
	history := make([]ConversationMessage, 60)
	for i := range history {
		history[i] = ConversationMessage{Role: "user", Content: strings.Repeat("\u4e2d", 2000)}
	}
	request := ConversationRequest{History: history, Resume: true}
	got := conversationRequestHistory(request)
	if len(got) != len(history) || got[0].Content != history[0].Content {
		t.Fatal("durable history was silently truncated")
	}
}
