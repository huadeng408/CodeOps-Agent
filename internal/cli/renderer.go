package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	panelMinWidth = 44
	panelMaxWidth = 92

	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
	ansiGray  = "\x1b[90m"
)

type StreamRenderer struct {
	mu            sync.Mutex
	out           io.Writer
	color         bool
	assistantOpen bool
}

func NewStreamRenderer(out io.Writer) *StreamRenderer {
	return &StreamRenderer{out: out, color: shouldUseColor(out)}
}

// StartAssistantPanel opens the streaming assistant panel header.
func (r *StreamRenderer) StartAssistantPanel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.assistantOpen {
		return
	}
	r.assistantOpen = true
	fmt.Fprint(r.out, r.paint(ansiBold, "\n  assistant")+" ")
}

// AppendAssistantText writes a text delta inside an open streaming panel.
func (r *StreamRenderer) AppendAssistantText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.assistantOpen {
		r.assistantOpen = true
		fmt.Fprint(r.out, r.paint(ansiBold, "\n  assistant")+" ")
	}
	fmt.Fprint(r.out, text)
}

// EndAssistantPanel closes a streaming assistant panel. No-op when none is open.
func (r *StreamRenderer) EndAssistantPanel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.assistantOpen {
		return
	}
	r.assistantOpen = false
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, r.paint(ansiCyan, "╰"+strings.Repeat("─", 72)+"╯"))
}

func (r *StreamRenderer) PrintLine(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(r.out)
		return
	}
	fmt.Fprintln(r.out, text)
}

func (r *StreamRenderer) PrintBlock(title string, lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.printPanel(title, lines)
}

func (r *StreamRenderer) PrintAssistant(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		lines = []string{"(empty response)"}
	}
	r.printPanel("assistant", lines)
}

func (r *StreamRenderer) PrintStatus(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintln(r.out, r.paint(ansiDim, "  "+text))
}

func (r *StreamRenderer) Separator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprintln(r.out, r.paint(ansiGray, strings.Repeat("─", 72)))
}

func (r *StreamRenderer) printPanel(title string, lines []string) {
	title = strings.TrimSpace(title)
	width := panelWidth(title, lines)
	header := "╭─ " + title + " " + strings.Repeat("─", max(width-runeLen(title)-1, 0)) + "╮"
	if title == "" {
		header = "╭" + strings.Repeat("─", width+2) + "╮"
	}

	fmt.Fprintln(r.out, r.paint(ansiCyan, header))
	for _, line := range wrapLines(lines, width) {
		fmt.Fprintln(r.out, r.paint(ansiCyan, "│")+" "+padRight(line, width)+" "+r.paint(ansiCyan, "│"))
	}
	fmt.Fprintln(r.out, r.paint(ansiCyan, "╰"+strings.Repeat("─", width+2)+"╯"))
}

func (r *StreamRenderer) paint(code, text string) string {
	if !r.color {
		return text
	}
	return code + text + ansiReset
}

func styledPrompt(out io.Writer) string {
	if !shouldUseColor(out) {
		return "╭─ You\n╰─> "
	}
	return ansiCyan + "╭─ " + ansiBold + "You" + ansiReset + "\n" + ansiCyan + "╰─>" + ansiReset + " "
}

func shouldUseColor(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("CLICOLOR") == "0" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	return file.Fd() == os.Stdout.Fd() || file.Fd() == os.Stderr.Fd()
}

func panelWidth(title string, lines []string) int {
	width := max(runeLen(title)+4, panelMinWidth)
	for _, line := range lines {
		width = max(width, min(runeLen(line), panelMaxWidth))
	}
	return min(width, panelMaxWidth)
}

func wrapLines(lines []string, width int) []string {
	if len(lines) == 0 {
		return []string{""}
	}
	out := []string{}
	for _, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		runes := []rune(line)
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		out = append(out, string(runes))
	}
	return out
}

func padRight(text string, width int) string {
	padding := width - runeLen(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func runeLen(text string) int {
	return len([]rune(text))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
