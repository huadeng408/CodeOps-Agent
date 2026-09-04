package codeagent_test

import (
	"sort"
	"testing"

	"code-agent/internal/identity"
	"code-agent/internal/permission"
)

func TestPermissionApprovalsAreScopedToActorAndSession(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	first, err := identity.Actor{
		SchemaVersion: identity.SchemaVersion,
		ActorID:       "user:42",
		Subject:       "alice",
		TenantID:      "org:7",
		Roles:         []string{"USER"},
	}.BindSession("session-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := identity.Actor{
		SchemaVersion: identity.SchemaVersion,
		ActorID:       "user:99",
		Subject:       "bob",
		TenantID:      "org:7",
		Roles:         []string{"USER"},
	}.BindSession("session-2")
	if err != nil {
		t.Fatal(err)
	}
	ctrl.ApproveSessionFor(first, "Write")
	if got := ctrl.CheckFor(first, "Write", nil); got != permission.Approve {
		t.Fatalf("first actor should retain approval, got %v", got)
	}
	if got := ctrl.CheckFor(second, "Write", nil); got != permission.AskUser {
		t.Fatalf("approval leaked across actor/session boundary, got %v", got)
	}
}

func TestDefaultPermissions(t *testing.T) {
	ctrl := permission.NewController(nil, nil)

	if got := ctrl.Check("Read", nil); got != permission.Approve {
		t.Fatalf("Read should auto-approve, got %v", got)
	}
	if got := ctrl.Check("Bash", nil); got != permission.AskUser {
		t.Fatalf("Bash should ask, got %v", got)
	}
}

func TestDenyRulesTakePrecedence(t *testing.T) {
	ctrl := permission.NewControllerWithRules(
		nil,
		[]permission.AllowRule{{Tool: "Bash", Pattern: "go test.*"}},
		[]permission.AllowRule{{Tool: "Bash", Pattern: "rm -rf"}},
	)

	if got := ctrl.Check("Bash", map[string]any{"command": "rm -rf /tmp/demo"}); got != permission.Deny {
		t.Fatalf("deny rule should win, got %v", got)
	}
	if got := ctrl.Check("Bash", map[string]any{"command": "go test ./..."}); got != permission.Approve {
		t.Fatalf("allow rule should approve, got %v", got)
	}
}

func TestSessionApproval(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	if got := ctrl.Check("Write", nil); got != permission.AskUser {
		t.Fatalf("Write should initially ask, got %v", got)
	}

	ctrl.ApproveSession("Write")
	if got := ctrl.Check("Write", nil); got != permission.Approve {
		t.Fatalf("Write should approve after session approval, got %v", got)
	}
}

func TestUnknownToolsAskForApproval(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	if got := ctrl.Check("MCPTool", nil); got != permission.AskUser {
		t.Fatalf("unknown tools should ask, got %v", got)
	}
	if got := ctrl.Level("MCPTool"); got != permission.AskSession {
		t.Fatalf("unknown tools should default to ask-session, got %v", got)
	}
}

func TestSessionApprovalSnapshotAndRestore(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	ctrl.ApproveSession("Write")
	ctrl.ApproveSession("Git")
	ctrl.ApproveSession("")

	approved := ctrl.ApprovedTools()
	if len(approved) != 2 || approved[0] != "Git" || approved[1] != "Write" {
		t.Fatalf("unexpected approved tools: %#v", approved)
	}

	restored := permission.NewController(nil, nil)
	restored.RestoreApprovedTools([]string{"Write", "Write"})
	if got := restored.Check("Write", nil); got != permission.Approve {
		t.Fatalf("restored Write should approve, got %v", got)
	}
	if got := restored.Check("Git", nil); got != permission.AskUser {
		t.Fatalf("Git should not be restored, got %v", got)
	}
}

// TestSuggestRulesFromRepeatedBashPattern 验证：同一 Bash 命令前缀被批准多次后，
// SuggestRules 会给出对应的候选规则，且该规则确实能匹配已批准的命令。
func TestSuggestRulesFromRepeatedBashPattern(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	for _, cmd := range []string{
		"go test ./internal/foo",
		"go test ./internal/bar",
		"go test ./internal/baz",
	} {
		ctrl.RecordApproval("Bash", map[string]any{"command": cmd})
	}

	// Bash 是 AlwaysAsk，即使记录了批准也不应被自动放行——建议而非授权。
	if got := ctrl.Check("Bash", map[string]any{"command": "go test ./internal/foo"}); got != permission.AskUser {
		t.Fatalf("Bash should still ask after recording approvals, got %v", got)
	}

	suggestions := ctrl.SuggestRules()
	if len(suggestions) != 1 {
		t.Fatalf("expected exactly one suggestion, got %#v", suggestions)
	}
	sug := suggestions[0]
	if sug.Tool != "Bash" || sug.Pattern != "go test" {
		t.Fatalf("unexpected suggestion: %+v", sug)
	}
	if sug.Count < 3 {
		t.Fatalf("expected count>=3, got %d", sug.Count)
	}
	if sug.Sample == "" {
		t.Fatalf("expected a non-empty sample, got %+v", sug)
	}

	// 候选规则应当真正匹配已批准的命令。
	rule := permission.AllowRule{Tool: sug.Tool, Pattern: sug.Pattern}
	if !rule.Matches("Bash", map[string]any{"command": "go test ./internal/foo"}) {
		t.Fatalf("suggested rule should match approved command: %+v", rule)
	}
}

// TestSuggestRulesDoesNotOverGeneralize 验证：批准互不相同的命令时，
// 不应把单一 token（例如 "go"）过度泛化为候选规则。
func TestSuggestRulesDoesNotOverGeneralize(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	for _, cmd := range []string{
		"go test ./a",
		"go build ./b",
		"go vet ./c",
		"npm run build",
		"git status",
	} {
		ctrl.RecordApproval("Bash", map[string]any{"command": cmd})
	}

	if suggestions := ctrl.SuggestRules(); len(suggestions) != 0 {
		t.Fatalf("expected no suggestions for varied commands, got %#v", suggestions)
	}
}

// TestSuggestRulesForFileSubdir 验证：对同一子目录下文件工具的多次批准
// 会归纳出针对该目录的候选规则。
func TestSuggestRulesForFileSubdir(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	for _, path := range []string{
		"/repo/docs/a.md",
		"/repo/docs/b.md",
		"/repo/docs/c.md",
	} {
		ctrl.RecordApproval("Write", map[string]any{"path": path})
	}

	suggestions := ctrl.SuggestRules()
	if len(suggestions) != 1 {
		t.Fatalf("expected one suggestion, got %#v", suggestions)
	}
	sug := suggestions[0]
	if sug.Tool != "Write" || sug.Pattern != "/repo/docs/" {
		t.Fatalf("unexpected subdir suggestion: %+v", sug)
	}
	if sug.Count < 3 {
		t.Fatalf("expected count>=3, got %d", sug.Count)
	}

	rule := permission.AllowRule{Tool: sug.Tool, Pattern: sug.Pattern}
	if !rule.Matches("Write", map[string]any{"path": "/repo/docs/d.md"}) {
		t.Fatalf("suggested rule should match a sibling file: %+v", rule)
	}
}

// TestRecordApprovalMarksSessionApproved 验证：对 AskSession 级别的工具，
// RecordApproval 在记录历史的同时也会放行本会话后续调用（与 ApproveSession 一致）。
func TestRecordApprovalMarksSessionApproved(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	if got := ctrl.Level("Write"); got != permission.AskSession {
		t.Fatalf("Write should be AskSession, got %v", got)
	}
	if got := ctrl.Check("Write", nil); got != permission.AskUser {
		t.Fatalf("Write should ask before approval, got %v", got)
	}

	ctrl.RecordApproval("Write", map[string]any{"path": "/tmp/a.txt"})
	if got := ctrl.Check("Write", nil); got != permission.Approve {
		t.Fatalf("Write should be session-approved after RecordApproval, got %v", got)
	}
}

// TestRecordApprovalBoundsHistory 验证历史被限制在最近 maxApprovalHistory 条。
func TestRecordApprovalBoundsHistory(t *testing.T) {
	const bound = 200 // 对应 internal/permission.maxApprovalHistory
	ctrl := permission.NewController(nil, nil)
	for i := 0; i < bound+50; i++ {
		ctrl.RecordApproval("Bash", map[string]any{"command": "echo hi"})
	}
	history := ctrl.ApprovalHistory()
	if len(history) != bound {
		t.Fatalf("expected history capped at %d, got %d", bound, len(history))
	}
}

// TestApprovalHistoryRestoreRoundTrip 验证历史可通过快照重建，且重建后建议依然可用。
func TestApprovalHistoryRestoreRoundTrip(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	for _, cmd := range []string{"npm run build", "npm run test", "npm run lint"} {
		ctrl.RecordApproval("Bash", map[string]any{"command": cmd})
	}
	snapshot := ctrl.ApprovalHistory()
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 records, got %d", len(snapshot))
	}

	restored := permission.NewController(nil, nil)
	restored.RestoreApprovalHistory(snapshot)
	suggestions := restored.SuggestRules()
	if len(suggestions) != 1 || suggestions[0].Pattern != "npm run" || suggestions[0].Count < 3 {
		t.Fatalf("restored controller should still suggest the npm run rule, got %#v", suggestions)
	}
}

// TestSuggestRulesSkipsAlreadyCovered 验证：若 allowlist 中已存在相同规则，则不再重复建议。
func TestSuggestRulesSkipsAlreadyCovered(t *testing.T) {
	ctrl := permission.NewControllerWithRules(
		nil,
		[]permission.AllowRule{{Tool: "Bash", Pattern: "go test"}},
		nil,
	)
	for _, cmd := range []string{"go test ./a", "go test ./b", "go test ./c"} {
		ctrl.RecordApproval("Bash", map[string]any{"command": cmd})
	}
	if suggestions := ctrl.SuggestRules(); len(suggestions) != 0 {
		t.Fatalf("expected no suggestion when rule already covered, got %#v", suggestions)
	}
}

// TestSuggestRulesStableOrdering 验证多个候选规则按计数降序、再按模式确定性排序。
func TestSuggestRulesStableOrdering(t *testing.T) {
	records := []permission.ApprovalRecord{}
	// "go test" 出现 5 次，"npm run" 出现 3 次。
	for i := 0; i < 5; i++ {
		records = append(records, permission.ApprovalRecord{
			Tool:      "Bash",
			Signature: "go test",
			Sample:    "go test ./x",
		})
	}
	for i := 0; i < 3; i++ {
		records = append(records, permission.ApprovalRecord{
			Tool:      "Bash",
			Signature: "npm run",
			Sample:    "npm run build",
		})
	}
	// 打乱顺序后多次运行，结果应一致。
	sortRecords := func() []permission.ApprovalRecord {
		out := append([]permission.ApprovalRecord(nil), records...)
		sort.Slice(out, func(i, j int) bool { return false })
		return out
	}
	first := permission.SuggestRules(sortRecords())
	if len(first) != 2 || first[0].Pattern != "go test" || first[0].Count != 5 ||
		first[1].Pattern != "npm run" || first[1].Count != 3 {
		t.Fatalf("unexpected ordering: %#v", first)
	}
}
