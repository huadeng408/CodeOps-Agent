package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"
)

type BootstrapView struct {
	Version, Branch, Workspace, Mode, Model string
}

type ToolEvent struct {
	ToolCallID                    string
	Name, Target, Summary, Detail string
	Error                         string
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
	assistantCol  int
	activeRow     *activeToolRow
}

type activeToolRow struct {
	id   string
	line string
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
		r.printWrappedLocked(left, r.theme.Strong)
		r.printWrappedLocked(right, r.theme.Muted)
	} else {
		fmt.Fprintln(r.out, r.paint(r.theme.Strong, left)+strings.Repeat(" ", gap)+r.paint(r.theme.Muted, right))
	}
	r.printWrappedLocked(strings.Trim(strings.TrimSpace(view.Mode)+" | "+strings.TrimSpace(view.Model)+" | /help", " |"), r.theme.Muted)
}

func (r *StreamRenderer) ToolStarted(name, target string) {
	r.ToolStartedWithID("", name, target)
}

func (r *StreamRenderer) ToolStartedWithID(toolCallID, name, target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := "* " + joinSubject(name, target) + " | running"
	if r.capabilities.Interactive && toolCallID != "" && r.activeRow == nil && !r.assistantOpen && cellWidth(line) <= r.capabilities.Width {
		r.activeRow = &activeToolRow{id: toolCallID, line: line}
		fmt.Fprint(r.out, "\r\x1b[2K"+r.paint(r.theme.Pending, line))
		return
	}
	// A second concurrent start cannot safely share the single ephemeral row.
	// Commit the first row before appending the durable parallel event.
	r.commitActiveLocked()
	r.printWrappedLocked(line, "")
}

func (r *StreamRenderer) ToolCompleted(event ToolEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	failed := event.ExitCode != 0 || strings.TrimSpace(event.Error) != ""
	marker, style := "[ok]", r.theme.Success
	parts := []string{marker + " " + joinSubject(event.Name, event.Target)}
	if failed {
		marker, style = "[fail]", r.theme.Danger
		parts[0] = marker + " " + joinSubject(event.Name, event.Target)
		if event.ExitCode != 0 {
			parts = append(parts, fmt.Sprintf("exit %d", event.ExitCode))
		}
	} else if strings.TrimSpace(event.Summary) != "" {
		parts = append(parts, sanitizeTerminalText(event.Summary))
	}
	if event.Duration > 0 {
		parts = append(parts, formatDuration(event.Duration))
	}
	line := strings.Join(parts, " | ")
	if r.activeRow != nil && r.capabilities.Interactive && r.activeRow.id == event.ToolCallID && cellWidth(line) <= r.capabilities.Width {
		fmt.Fprint(r.out, "\r\x1b[2K"+r.paint(style, line)+"\n")
		r.activeRow = nil
	} else {
		r.commitActiveLocked()
		r.printTokenLineLocked(parts, style)
	}
	if failed {
		detailText := strings.TrimSpace(event.Error)
		if strings.TrimSpace(event.Detail) != "" && strings.TrimSpace(event.Detail) != detailText {
			if detailText != "" {
				detailText += "\n"
			}
			detailText += strings.TrimSpace(event.Detail)
		}
		for _, detail := range wrapCells(detailText, max(r.capabilities.Width-2, 1)) {
			if detail != "" {
				r.printWrappedLocked("  "+detail, "")
			}
		}
		if event.Truncated {
			r.printWrappedLocked("  [output truncated]", "")
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
	r.appendAssistantLocked(text)
}

func (r *StreamRenderer) EndAssistant() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.assistantOpen {
		fmt.Fprintln(r.out)
		r.assistantOpen = false
		r.assistantCol = 0
	}
}

func (r *StreamRenderer) PrintPermission(view PermissionView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	r.printWrappedLocked("Permission required", r.theme.Strong)
	r.printWrappedLocked(joinSubject(view.Tool, view.Reason), "")
	if strings.TrimSpace(view.Parameters) != "" {
		fmt.Fprintln(r.out)
		r.printWrappedLocked("  "+view.Parameters, "")
	}
	fmt.Fprintln(r.out)
	if view.AllowSession {
		r.printWrappedLocked("[y] Allow session  [n] Deny", "")
	} else {
		r.printWrappedLocked("[y] Allow once  [n] Deny", "")
	}
}

func (r *StreamRenderer) PrintQuestion(view QuestionView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	r.printWrappedLocked("Question", r.theme.Strong)
	r.printWrappedLocked(view.Question, "")
	for index, option := range view.Options {
		line := fmt.Sprintf("[%d] %s", index+1, sanitizeTerminalText(option.Label))
		if option.Description != "" {
			line += " | " + sanitizeTerminalText(option.Description)
		}
		r.printWrappedLocked(line, "")
		if option.Preview != "" {
			r.printWrappedLocked("    "+option.Preview, "")
		}
	}
}

func (r *StreamRenderer) PrintDiff(view DiffSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	r.printWrappedLocked(fmt.Sprintf("Changes | %d files | +%d -%d", view.Files, view.Added, view.Removed), "")
	for _, line := range view.Lines {
		r.printWrappedLocked(line, "")
	}
	if view.Truncated {
		r.printWrappedLocked("[diff truncated]", "")
	}
}

func (r *StreamRenderer) PrintError(text string) {
	r.printSemanticLine("[error] ", text, r.theme.Danger)
}
func (r *StreamRenderer) PrintInterrupted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.assistantOpen {
		fmt.Fprintln(r.out)
		r.assistantOpen = false
		r.assistantCol = 0
	}
	r.commitActiveLocked()
	r.printWrappedLocked("[interrupted] Ready for new instructions.", r.theme.Pending)
}

func (r *StreamRenderer) PrintLine(text string) { r.printSemanticLine("", text, "") }
func (r *StreamRenderer) PrintStatus(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitActiveLocked()
	fields := strings.Split(strings.TrimSpace(text), " | ")
	joined := strings.Join(fields, " | ")
	if cellWidth(joined) <= r.capabilities.Width {
		fmt.Fprintln(r.out, r.paint(r.theme.Muted, joined))
		return
	}
	for len(fields) > 3 && cellWidth(strings.Join(fields, " | ")) > r.capabilities.Width {
		fields = fields[:len(fields)-1]
	}
	for _, field := range fields {
		if cellWidth(field) <= r.capabilities.Width {
			fmt.Fprintln(r.out, r.paint(r.theme.Muted, field))
		}
	}
}
func (r *StreamRenderer) Separator() {}
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
	r.printWrappedLocked(prefix+text, style)
}

func (r *StreamRenderer) startAssistantLocked() {
	if r.assistantOpen {
		return
	}
	r.commitActiveLocked()
	r.assistantOpen = true
	prefix := "assistant > "
	fmt.Fprint(r.out, r.paint(r.theme.Strong, prefix))
	r.assistantCol = cellWidth(prefix)
}

func (r *StreamRenderer) appendAssistantLocked(text string) {
	text = sanitizeTerminalText(text)
	for len(text) > 0 {
		if text[0] == '\n' {
			fmt.Fprintln(r.out)
			r.assistantCol = 0
			text = text[1:]
			continue
		}
		newline := strings.IndexByte(text, '\n')
		segment := text
		if newline >= 0 {
			segment = text[:newline]
		}
		if r.assistantCol >= r.capabilities.Width {
			fmt.Fprintln(r.out)
			r.assistantCol = 0
		}
		remaining := max(r.capabilities.Width-r.assistantCol, 1)
		clusters := graphemes.FromString(segment)
		if clusters.Next() {
			firstWidth := runewidth.StringWidth(clusters.Value())
			if firstWidth > remaining && r.assistantCol > 0 {
				fmt.Fprintln(r.out)
				r.assistantCol = 0
				remaining = r.capabilities.Width
			}
		}
		lines := wrapCells(segment, remaining)
		for index, line := range lines {
			if index > 0 {
				fmt.Fprintln(r.out)
				r.assistantCol = 0
			}
			fmt.Fprint(r.out, line)
			r.assistantCol += cellWidth(line)
			remaining = r.capabilities.Width
		}
		text = text[len(segment):]
		if newline < 0 {
			break
		}
	}
}

func (r *StreamRenderer) commitActiveLocked() {
	if r.activeRow == nil {
		return
	}
	if r.capabilities.Interactive {
		fmt.Fprint(r.out, "\n")
	}
	r.activeRow = nil
}

func (r *StreamRenderer) printWrappedLocked(text, style string) {
	for _, line := range wrapCells(text, r.capabilities.Width) {
		fmt.Fprintln(r.out, r.paint(style, line))
	}
}

func (r *StreamRenderer) printTokenLineLocked(tokens []string, style string) {
	line := ""
	flush := func() {
		if line != "" {
			fmt.Fprintln(r.out, r.paint(style, line))
			line = ""
		}
	}
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		candidate := token
		if line != "" {
			candidate = line + " | " + token
		}
		if line != "" && cellWidth(candidate) > r.capabilities.Width {
			flush()
		}
		if cellWidth(token) > r.capabilities.Width {
			for _, wrapped := range wrapCells(token, r.capabilities.Width) {
				fmt.Fprintln(r.out, r.paint(style, wrapped))
			}
			continue
		}
		if line == "" {
			line = token
		} else {
			line += " | " + token
		}
	}
	flush()
}

func (r *StreamRenderer) Width() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.capabilities.Width
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
