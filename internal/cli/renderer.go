package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type BootstrapView struct {
	Version, Branch, Workspace, Mode, Model string
}

type ToolEvent struct {
	Name, Target, Summary, Detail string
	ExitCode                      int
	Duration                      time.Duration
	Truncated                     bool
}

type PermissionView struct {
	Tool, Reason, Parameters string
	AllowSession             bool
}

type QuestionOption struct{ Label, Description, Preview string }
type QuestionView struct {
	Question    string
	Options     []QuestionOption
	MultiSelect bool
}

type DiffSummary struct {
	Files, Added, Removed int
	Lines                 []string
	Truncated             bool
}

type StreamRenderer struct {
	mu            sync.Mutex
	out           io.Writer
	capabilities  TerminalCapabilities
	theme         Theme
	assistantOpen bool
	activeRow     string
}

func NewStreamRenderer(out io.Writer) *StreamRenderer {
	return NewStreamRendererWithCapabilities(out, detectTerminalCapabilities(out, defaultCapabilityEnv()))
}

func NewStreamRendererWithCapabilities(out io.Writer, capabilities TerminalCapabilities) *StreamRenderer {
	if capabilities.Width <= 0 {
		capabilities.Width = fallbackTerminalWidth
	}
	return &StreamRenderer{out: out, capabilities: capabilities, theme: defaultTheme()}
}

func (r *StreamRenderer) PrintBootstrap(view BootstrapView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	left := "code-agent " + strings.TrimSpace(view.Version)
	right := strings.Trim(strings.TrimSpace(view.Branch)+" | "+strings.TrimSpace(view.Workspace), " |")
	gap := r.capabilities.Width - cellWidth(left) - cellWidth(right)
	if gap < 1 {
		fmt.Fprintln(r.out, r.paint(r.theme.Strong, left))
		fmt.Fprintln(r.out, r.paint(r.theme.Muted, right))
	} else {
		fmt.Fprintln(r.out, r.paint(r.theme.Strong, left)+strings.Repeat(" ", gap)+r.paint(r.theme.Muted, right))
	}
	fmt.Fprintln(r.out, r.paint(r.theme.Muted, strings.Trim(strings.TrimSpace(view.Mode)+" | "+strings.TrimSpace(view.Model)+" | /help", " |")))
}

func (r *StreamRenderer) ToolStarted(name, target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := "* " + joinSubject(name, target) + " | running"
	if r.capabilities.Interactive && r.activeRow == "" && !r.assistantOpen {
		r.activeRow = line
		fmt.Fprint(r.out, "\r\x1b[2K"+r.paint(r.theme.Pending, line))
		return
	}
	fmt.Fprintln(r.out, line)
}

func (r *StreamRenderer) ToolCompleted(event ToolEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	failed := event.ExitCode != 0
	marker, style := "[ok]", r.theme.Success
	parts := []string{marker + " " + joinSubject(event.Name, event.Target)}
	if failed {
		marker, style = "[fail]", r.theme.Danger
		parts[0] = marker + " " + joinSubject(event.Name, event.Target)
		parts = append(parts, fmt.Sprintf("exit %d", event.ExitCode))
	} else if strings.TrimSpace(event.Summary) != "" {
		parts = append(parts, sanitizeTerminalText(event.Summary))
	}
	if event.Duration > 0 {
		parts = append(parts, formatDuration(event.Duration))
	}
	line := strings.Join(parts, " | ")
	if r.activeRow != "" && r.capabilities.Interactive {
		fmt.Fprint(r.out, "\r\x1b[2K"+r.paint(style, line)+"\n")
		r.activeRow = ""
	} else {
		fmt.Fprintln(r.out, r.paint(style, line))
	}
	if failed {
		for _, detail := range wrapCells(event.Detail, max(r.capabilities.Width-2, 1)) {
			if detail != "" {
				fmt.Fprintln(r.out, "  "+detail)
			}
		}
		if event.Truncated {
			fmt.Fprintln(r.out, "  [output truncated]")
		}
	}
}

func (r *StreamRenderer) StartAssistant() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startAssistantLocked()
}

func (r *StreamRenderer) AppendAssistantText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startAssistantLocked()
	fmt.Fprint(r.out, sanitizeTerminalText(text))
}

func (r *StreamRenderer) EndAssistant() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.assistantOpen {
		fmt.Fprintln(r.out)
		r.assistantOpen = false
	}
}

func (r *StreamRenderer) PrintPermission(view PermissionView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	fmt.Fprintln(r.out, r.paint(r.theme.Strong, "Permission required"))
	fmt.Fprintln(r.out, joinSubject(view.Tool, view.Reason))
	if strings.TrimSpace(view.Parameters) != "" {
		fmt.Fprintln(r.out, "\n  "+sanitizeTerminalText(view.Parameters))
	}
	choices := "[1] Allow once"
	if view.AllowSession {
		choices += "  [2] Allow session"
	}
	fmt.Fprintln(r.out, "\n"+choices+"  [Esc] Deny")
}

func (r *StreamRenderer) PrintQuestion(view QuestionView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	fmt.Fprintln(r.out, r.paint(r.theme.Strong, "Question"))
	fmt.Fprintln(r.out, sanitizeTerminalText(view.Question))
	for index, option := range view.Options {
		line := fmt.Sprintf("[%d] %s", index+1, sanitizeTerminalText(option.Label))
		if option.Description != "" {
			line += " | " + sanitizeTerminalText(option.Description)
		}
		fmt.Fprintln(r.out, line)
		if option.Preview != "" {
			fmt.Fprintln(r.out, "    "+sanitizeTerminalText(option.Preview))
		}
	}
}

func (r *StreamRenderer) PrintDiff(view DiffSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	fmt.Fprintf(r.out, "Changes | %d files | +%d -%d\n", view.Files, view.Added, view.Removed)
	for _, line := range view.Lines {
		fmt.Fprintln(r.out, sanitizeTerminalText(line))
	}
	if view.Truncated {
		fmt.Fprintln(r.out, "[diff truncated]")
	}
}

func (r *StreamRenderer) PrintError(text string) {
	r.printSemanticLine("[error] ", text, r.theme.Danger)
}
func (r *StreamRenderer) PrintInterrupted() {
	r.printSemanticLine("", "[interrupted] Ready for new instructions.", r.theme.Pending)
}

func (r *StreamRenderer) PrintLine(text string)   { r.printSemanticLine("", text, "") }
func (r *StreamRenderer) PrintStatus(text string) { r.printSemanticLine("", text, r.theme.Muted) }
func (r *StreamRenderer) Separator()              {}
func (r *StreamRenderer) PrintBlock(title string, lines []string) {
	r.PrintLine(strings.TrimSpace(title))
	for _, line := range lines {
		r.PrintLine("  " + line)
	}
}
func (r *StreamRenderer) PrintAssistant(text string) { r.AppendAssistantText(text); r.EndAssistant() }
func (r *StreamRenderer) StartAssistantPanel()       { r.StartAssistant() }
func (r *StreamRenderer) EndAssistantPanel()         { r.EndAssistant() }

func (r *StreamRenderer) printSemanticLine(prefix, text, style string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	fmt.Fprintln(r.out, r.paint(style, prefix+sanitizeTerminalText(text)))
}

func (r *StreamRenderer) startAssistantLocked() {
	if r.assistantOpen {
		return
	}
	r.commitActiveLocked()
	r.assistantOpen = true
	fmt.Fprint(r.out, r.paint(r.theme.Strong, "assistant > "))
}

func (r *StreamRenderer) commitActiveLocked() {
	if r.activeRow == "" {
		return
	}
	if r.capabilities.Interactive {
		fmt.Fprint(r.out, "\n")
	}
	r.activeRow = ""
}

func (r *StreamRenderer) paint(code, text string) string {
	if !r.capabilities.Color || code == "" {
		return text
	}
	return code + text + ansiReset
}

func joinSubject(name, target string) string {
	return strings.TrimSpace(sanitizeTerminalText(name) + " " + sanitizeTerminalText(target))
}

func formatDuration(duration time.Duration) string {
	return fmt.Sprintf("%.1fs", duration.Seconds())
}

func styledPrompt(out io.Writer) string {
	capabilities := detectTerminalCapabilities(out, defaultCapabilityEnv())
	if !capabilities.Color {
		return "> "
	}
	return ansiCyan + ">" + ansiReset + " "
}
