package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/config"
	"code-agent/internal/hooks"
	"code-agent/internal/metrics"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/safety"
	"code-agent/internal/session"
	"code-agent/internal/skills"
	"code-agent/internal/tools"
	"code-agent/internal/undo"
	"code-agent/internal/worktree"
)

func TestHandleToolCallPromptsAndApprovesAskSessionTool(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "y\n")

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:               "Write",
		RequiredPermission: 2,
		ParametersJSON:     `{"path":"notes.txt","content":"approved"}`,
	})

	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("expected write to run after approval, got result=%+v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "approved" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
	if got := app.permissions.Check("Write", nil); got != permission.Approve {
		t.Fatalf("Write should be approved for the session, got %v", got)
	}
	if !strings.Contains(out.String(), "Permission required") || !strings.Contains(out.String(), "[y] Allow session") || !strings.Contains(out.String(), "[n] Deny") {
		t.Fatalf("permission prompt was not rendered: %q", out.String())
	}
}

func TestHandleToolCallPersistsApprovalAuditRecord(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "y\n")

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:               "Write",
		RequiredPermission: 2,
		ParametersJSON:     `{"path":"docs/audit.txt","content":"recorded"}`,
	})
	if result.ExitCode != 0 {
		t.Fatalf("approved write failed: %+v", result)
	}

	history := app.permissions.ApprovalHistory()
	if len(history) != 1 {
		t.Fatalf("approval history length = %d, want 1", len(history))
	}
	if history[0].Tool != "Write" || history[0].Signature == "" || history[0].Sample == "" {
		t.Fatalf("approval audit record is incomplete: %+v", history[0])
	}
	current := app.session.Current()
	if len(current.ApprovalHistory) != 1 || current.ApprovalHistory[0].Tool != "Write" {
		t.Fatalf("session did not persist approval history: %+v", current.ApprovalHistory)
	}
}

func TestRestorePermissionsRestoresApprovalAuditHistory(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "")
	record := permission.ApprovalRecord{
		Tool:      "Write",
		Signature: "path:docs",
		Sample:    "docs/README.md",
		Timestamp: time.Unix(123, 0).UTC(),
	}

	app.restorePermissions(session.Session{
		ApprovedTools:   []string{"Write"},
		ApprovalHistory: []permission.ApprovalRecord{record},
	})

	history := app.permissions.ApprovalHistory()
	if len(history) != 1 || history[0] != record {
		t.Fatalf("approval audit history was not restored: %+v", history)
	}
}

func TestApprovalAnswerOnlyAcceptsExplicitY(t *testing.T) {
	for _, answer := range []string{"y", "Y"} {
		if !isApprovalAnswer(answer) {
			t.Fatalf("%q should allow the session", answer)
		}
	}
	for _, answer := range []string{"yes", "allow", "approve", "ok", "1", "", "n", "esc"} {
		if isApprovalAnswer(answer) {
			t.Fatalf("%q must not be accepted as an approval", answer)
		}
	}
}

func TestConfirmToolApprovalUsesPermissionLevelForPrompt(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "y\ny\n")

	if ok, err := app.confirmToolApproval(context.Background(), orchestrator.ToolCall{Name: "Write", ParametersJSON: `{"path":"a.txt"}`}, nil); err != nil || !ok {
		t.Fatalf("AskSession approval failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out.String(), "[y] Allow session") {
		t.Fatalf("AskSession prompt should offer session approval: %q", out.String())
	}

	out.Reset()
	if ok, err := app.confirmToolApproval(context.Background(), orchestrator.ToolCall{Name: "Bash", ParametersJSON: `{"command":"echo hi"}`}, nil); err != nil || !ok {
		t.Fatalf("AlwaysAsk approval failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out.String(), "[y] Allow once") || strings.Contains(out.String(), "Allow session") {
		t.Fatalf("AlwaysAsk prompt should be one-shot: %q", out.String())
	}
}

func TestNewAppWiresIdleInterruptHandler(t *testing.T) {
	root := t.TempDir()
	app := NewApp(config.Config{ProjectRoot: root, WorkingDir: root, SessionDBPath: filepath.Join(root, "sessions.db")}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if app.input == nil || app.input.onInterrupt == nil {
		t.Fatal("NewApp must wire idle Ctrl+C to App interrupt handling")
	}
}

func TestHandleOrchestratorEventPreservesToolCallIDAndError(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "")
	app.renderer = NewStreamRendererWithCapabilities(out, TerminalCapabilities{Interactive: true, Width: 80})

	app.handleOrchestratorEvent(context.Background(), orchestrator.Event{ToolProgress: &orchestrator.ToolProgress{ToolCallID: "call-a", ToolName: "Read", Phase: "start"}})
	app.handleOrchestratorEvent(context.Background(), orchestrator.Event{ToolProgress: &orchestrator.ToolProgress{ToolCallID: "call-b", ToolName: "Read", Phase: "start"}})
	app.handleOrchestratorEvent(context.Background(), orchestrator.Event{ToolProgress: &orchestrator.ToolProgress{ToolCallID: "call-b", ToolName: "Read", Phase: "finish", Error: "read failed"}})
	app.handleOrchestratorEvent(context.Background(), orchestrator.Event{ToolProgress: &orchestrator.ToolProgress{ToolCallID: "call-a", ToolName: "Read", Phase: "finish"}})

	got := out.String()
	if !strings.Contains(got, "* Read | running\n* Read | running\n") || !strings.Contains(got, "[fail] Read") || !strings.Contains(got, "read failed") {
		t.Fatalf("progress lifecycle was not durable and attributed: %q", got)
	}
	if strings.Index(got, "[fail] Read") > strings.LastIndex(got, "[ok] Read") {
		t.Fatalf("out-of-order completion order was not preserved: %q", got)
	}
}

func TestHandleToolCallDeniesWhenUserDeclines(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "n\n")

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:               "Write",
		RequiredPermission: 2,
		ParametersJSON:     `{"path":"notes.txt","content":"denied"}`,
	})

	if result.ExitCode == 0 || result.Error != "permission denied by user" {
		t.Fatalf("expected user denial, got result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied write should not create file, stat err=%v", err)
	}
}

func TestHandleAskUserRequestSelectsOption(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "2\n")

	result, err := app.handleAskUserRequest(context.Background(), &codeagentpb.AskUserRequest{
		Question: "Choose a path",
		Options: []*codeagentpb.Option{
			{Label: "alpha", Description: "first choice"},
			{Label: "beta", Preview: "second"},
		},
		AskUserId: "ask-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolName != "AskUser" || result.ToolCallID != "ask-1" || result.Output != "beta" || result.ExitCode != 0 {
		t.Fatalf("unexpected ask user result: %+v", result)
	}
	if !strings.Contains(out.String(), "Question") || !strings.Contains(out.String(), "[1] alpha") || !strings.Contains(out.String(), "[2] beta") {
		t.Fatalf("ask user prompt was not rendered: %q", out.String())
	}
}

func TestHandleAskUserRequestSupportsMultiSelect(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "1,2\n")

	result, err := app.handleAskUserRequest(context.Background(), &codeagentpb.AskUserRequest{
		Question: "Pick multiple",
		Options: []*codeagentpb.Option{
			{Label: "alpha"},
			{Label: "beta"},
		},
		MultiSelect: true,
		AskUserId:   "ask-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != `["alpha","beta"]` || result.ExitCode != 0 {
		t.Fatalf("unexpected multi-select result: %+v", result)
	}
}

func TestHandleInterruptCancelsCurrentTurn(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "")
	turnCtx, turnCancel := context.WithCancel(context.Background())
	app.setCurrentCancel(turnCancel)
	stopped := false

	if app.handleInterrupt(time.Now(), func() { stopped = true }) {
		t.Fatal("first interrupt should not request shutdown")
	}
	if turnCtx.Err() == nil {
		t.Fatal("current turn was not cancelled")
	}
	if stopped {
		t.Fatal("first interrupt should not stop the app")
	}
	if !strings.Contains(out.String(), "[interrupted] Ready for new instructions.") {
		t.Fatalf("interrupt prompt was not rendered: %q", out.String())
	}
}

func TestHandleInterruptStopsAfterThreeRapidInterrupts(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "")
	runCtx, stop := context.WithCancel(context.Background())
	now := time.Now()

	app.handleInterrupt(now, stop)
	app.handleInterrupt(now.Add(300*time.Millisecond), stop)
	if !app.handleInterrupt(now.Add(600*time.Millisecond), stop) {
		t.Fatal("third rapid interrupt should request shutdown")
	}
	if runCtx.Err() == nil {
		t.Fatal("run context was not cancelled")
	}
}

func TestHandleSlashCommandListsSkills(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()

	if !app.handleSlashCommand(context.Background(), "/skills") {
		t.Fatal("/skills should be handled")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "/review") || !strings.Contains(rendered, "/security-review") || !strings.Contains(rendered, "/init") {
		t.Fatalf("skills list did not include expected commands: %q", rendered)
	}
}

func TestHandleSlashCommandDiffUsesStructuredSummary(t *testing.T) {
	root := newGitTestRepo(t)
	path := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "add", "tracked.txt")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	cmd = exec.Command("git", "-C", root, "commit", "-m", "add tracked")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, out := newPermissionTestApp(root, "")
	app.worktree = worktree.NewManager(root, "HEAD")
	if !app.handleSlashCommand(context.Background(), "/diff") {
		t.Fatal("/diff should be handled")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Changes | 1 files | +1 -0") || !strings.Contains(rendered, "M tracked.txt") {
		t.Fatalf("expected deterministic structured diff, got %q", rendered)
	}
	if strings.Contains(rendered, "diff\n") {
		t.Fatalf("legacy generic diff block should not be rendered: %q", rendered)
	}
}

func TestHandleSlashCommandRunsSkillConversation(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()

	if !app.handleSlashCommand(context.Background(), "/review focus on auth changes") {
		t.Fatal("/review should be handled")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Run the review skill.") || !strings.Contains(rendered, "focus on auth changes") {
		t.Fatalf("skill prompt was not sent through the conversation path: %q", rendered)
	}
	messages := app.session.Current().Messages
	if len(messages) < 2 || messages[len(messages)-2].Role != session.RoleUser || !strings.Contains(messages[len(messages)-2].Content, "/review") {
		t.Fatalf("skill command was not recorded in session messages: %+v", messages)
	}
}

func TestHandleSlashCommandRunsNamedSkill(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()
	app.skills.Register(skills.Skill{
		Name:        "deploy",
		Description: "Deploy the current revision.",
		Prompt:      "Check the release before deploying.",
		Tools:       []string{"Git"},
	})

	if !app.handleSlashCommand(context.Background(), "/skill deploy production") {
		t.Fatal("/skill should be handled")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Run the deploy skill.") || !strings.Contains(rendered, "production") {
		t.Fatalf("named skill prompt was not sent through the conversation path: %q", rendered)
	}
	messages := app.session.Current().Messages
	if len(messages) < 2 || !strings.Contains(messages[len(messages)-2].Content, "/skill deploy production") {
		t.Fatalf("generic command was not recorded in session messages: %+v", messages)
	}
}

func TestBuildSkillInputIncludesPromptToolsAndFocus(t *testing.T) {
	input := buildSkillInput(skills.Skill{
		Name:   "review",
		Prompt: "Review the current changes.",
		Tools:  []string{"Read", "Git"},
	}, "only changed files")

	if !strings.Contains(input, "Review the current changes.") ||
		!strings.Contains(input, "Preferred tools: Read, Git") ||
		!strings.Contains(input, "User focus:") ||
		!strings.Contains(input, "only changed files") {
		t.Fatalf("unexpected skill input: %q", input)
	}
}

func TestSecurityReviewCommandRecordsPublicCommandName(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()

	if !app.handleSlashCommand(context.Background(), "/security-review check shell usage") {
		t.Fatal("/security-review should be handled")
	}
	messages := app.session.Current().Messages
	if len(messages) < 2 || !strings.Contains(messages[len(messages)-2].Content, "/security-review") {
		t.Fatalf("public command name was not recorded: %+v", messages)
	}
}

func newPermissionTestApp(root, input string) (*App, *bytes.Buffer) {
	out := &bytes.Buffer{}
	sessions := session.NewManager(session.NewMemoryStore())
	sessions.NewSession(root)

	return &App{
		cfg:         config.Config{ProjectRoot: root, WorkingDir: root},
		input:       NewInputBuffer(strings.NewReader(input), out),
		renderer:    NewStreamRenderer(out),
		status:      NewStatusLine(),
		metrics:     metrics.NewCollector(),
		session:     sessions,
		permissions: permission.NewController(nil, nil),
		hooks:       hooks.NewEngine(),
		executor:    tools.NewExecutor(root),
		safety:      safety.NewAnalyzer(),
		undo:        undo.NewManager(),
	}, out
}
