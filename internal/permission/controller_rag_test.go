package permission

import "testing"

func TestDefaultPermissionsAutoAllowSearchKnowledge(t *testing.T) {
	controller := NewController(nil, nil)
	if got := controller.Level("SearchKnowledge"); got != AutoAllow {
		t.Fatalf("SearchKnowledge level = %v, want AutoAllow", got)
	}
	if got := controller.Check("SearchKnowledge", map[string]any{"query": "mineru"}); got != Approve {
		t.Fatalf("SearchKnowledge decision = %v, want Approve", got)
	}
}
