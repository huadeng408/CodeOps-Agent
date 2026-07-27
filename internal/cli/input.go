package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"
)

const defaultHistorySize = 100

// Interrupt counter tuning. These mirror App.handleInterrupt so a triple
// Ctrl+C at the idle prompt force-quits exactly like it does mid-turn.
const (
	interruptWindow         = time.Second
	interruptForceThreshold = 3
)

type completionCandidate struct {
	name  string
	isDir bool
}

// HistoryRing is a fixed-capacity ring buffer for input history.
type HistoryRing struct {
	entries []string
	maxSize int
}

// NewHistoryRing creates a history ring with the given maximum size.
func NewHistoryRing(maxSize int) *HistoryRing {
	if maxSize <= 0 {
		maxSize = defaultHistorySize
	}
	return &HistoryRing{
		entries: make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add appends a line to history. Empty lines and consecutive duplicates are
// skipped.
func (h *HistoryRing) Add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == line {
		return
	}
	h.entries = append(h.entries, line)
	if len(h.entries) > h.maxSize {
		h.entries = h.entries[len(h.entries)-h.maxSize:]
	}
}

// Len returns the number of entries in the ring.
func (h *HistoryRing) Len() int { return len(h.entries) }

// Get returns the entry at index idx (0 = oldest).
func (h *HistoryRing) Get(idx int) string {
	if idx < 0 || idx >= len(h.entries) {
		return ""
	}
	return h.entries[idx]
}

// InputBuffer reads lines from an io.Reader. When the reader is a terminal,
// raw-mode line editing with history, cursor movement, and tab completion is
// used. Otherwise it falls back to a simple buffered read.
type InputBuffer struct {
	reader  *bufio.Reader
	writer  io.Writer
	prompt  string
	history *HistoryRing
	tty     *os.File // nil when stdin is not a terminal

	// workspaceDir, when set via SetWorkspaceDir, is the directory tab
	// completion resolves candidates against (the configured workspace root)
	// instead of the process CWD. Empty falls back to "." (process CWD), which
	// keeps completion behaviour identical when CWD == workspace.
	workspaceDir string

	// onInterrupt, when set, is invoked when the user presses Ctrl+C (0x03) at
	// the idle prompt in raw mode. It returns true to request a force-quit.
	// When nil, InputBuffer falls back to its own consecutive-interrupt
	// counter (recordInterrupt), mirroring App.handleInterrupt so three rapid
	// Ctrl+C presses force-quit the program.
	onInterrupt   func() bool
	interrupts    int
	lastInterrupt time.Time
}

// NewInputBuffer creates an InputBuffer. If in is a terminal, raw-mode line
// editing is enabled automatically.
func NewInputBuffer(in io.Reader, out io.Writer) *InputBuffer {
	ib := &InputBuffer{
		writer:  out,
		prompt:  styledPrompt(out),
		history: NewHistoryRing(defaultHistorySize),
	}

	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		ib.tty = f
	} else {
		ib.reader = bufio.NewReader(in)
	}

	return ib
}

// SetInterruptHandler installs an optional callback invoked when the user
// presses Ctrl+C at the idle prompt in raw mode. When set, it takes priority
// over InputBuffer's built-in consecutive-interrupt counter, letting the App
// share one counter between the mid-turn (signal) and idle-prompt (0x03) paths.
// The callback returns true to force-quit the program.
func (b *InputBuffer) SetInterruptHandler(fn func() bool) {
	b.onInterrupt = fn
}

// SetWorkspaceDir configures the directory used for tab path completion. When
// set, doPathCompletion lists entries under dir instead of the process CWD, so
// completion matches the workspace the agent is operating on even when the
// harness was launched from a different directory. When unset (""), completion
// falls back to the process CWD, preserving the original behaviour.
//
// Wiring followup: NewApp should call a.input.SetWorkspaceDir(cfg.WorkingDir)
// once the InputBuffer is constructed so completion always targets the loaded
// workspace; this method is intentionally side-effect-free so that call can be
// added without touching any other input.go logic.
func (b *InputBuffer) SetWorkspaceDir(dir string) {
	b.workspaceDir = dir
}

// recordInterrupt tracks consecutive Ctrl+C presses at the idle prompt and
// mirrors App.handleInterrupt's counter semantics: presses separated by more
// than interruptWindow reset the count; reaching interruptForceThreshold rapid
// presses returns true to signal a force-quit. readLineEditor runs on a single
// goroutine, so no locking is required here.
func (b *InputBuffer) recordInterrupt(now time.Time) bool {
	if b.lastInterrupt.IsZero() || now.Sub(b.lastInterrupt) > interruptWindow {
		b.interrupts = 0
	}
	b.interrupts++
	b.lastInterrupt = now
	return b.interrupts >= interruptForceThreshold
}

// ReadLine reads a line of input. When attached to a terminal, raw-mode line
// editing with history navigation, cursor movement, and tab completion is used.
// Falls back to a simple buffered read when stdin is not a terminal.
func (b *InputBuffer) ReadLine(ctx context.Context) (string, error) {
	if b.tty != nil {
		return b.readLineEditor(ctx)
	}
	return b.readLineSimple(ctx)
}

// readLineSimple is the fallback: buffered read with context support.
func (b *InputBuffer) readLineSimple(ctx context.Context) (string, error) {
	fmt.Fprint(b.writer, b.prompt)

	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := b.reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			ch <- result{err: err}
			return
		}
		ch <- result{line: strings.TrimRight(line, "\r\n"), err: err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if errors.Is(res.err, io.EOF) && res.line == "" {
			return "", io.EOF
		}
		return res.line, res.err
	}
}

// readLineEditor runs the raw-mode line editor on the terminal.
func (b *InputBuffer) readLineEditor(ctx context.Context) (string, error) {
	fd := int(b.tty.Fd())

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return b.readLineSimple(ctx)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
	}()

	fmt.Fprint(b.writer, b.prompt)

	var buf []rune
	cursor := 0
	historyIdx := -1 // -1 means editing the current (unsaved) line
	savedLine := ""

	for {
		select {
		case <-ctx.Done():
			fmt.Fprint(b.writer, "\r\n")
			return "", ctx.Err()
		default:
		}

		key, escSeq, isEscape, err := b.readTermKey()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", io.EOF
			}
			return "", err
		}

		if isEscape {
			switch escSeq {
			case "[A": // Up arrow
				if b.history.Len() == 0 {
					continue
				}
				if historyIdx == -1 {
					savedLine = string(buf)
					historyIdx = b.history.Len() - 1
				} else if historyIdx > 0 {
					historyIdx--
				}
				buf = []rune(b.history.Get(historyIdx))
				cursor = len(buf)

			case "[B": // Down arrow
				if historyIdx == -1 {
					continue
				}
				if historyIdx < b.history.Len()-1 {
					historyIdx++
					buf = []rune(b.history.Get(historyIdx))
				} else {
					historyIdx = -1
					buf = []rune(savedLine)
				}
				cursor = len(buf)

			case "[C": // Right arrow
				if cursor < len(buf) {
					cursor++
				}
			case "[D": // Left arrow
				if cursor > 0 {
					cursor--
				}
			case "[H": // Home
				cursor = 0
			case "[F": // End
				cursor = len(buf)
			case "[3~": // Delete key
				if cursor < len(buf) {
					buf = append(buf[:cursor], buf[cursor+1:]...)
				}
			}

		} else {
			switch key {
			case '\r': // Enter
				line := string(buf)
				fmt.Fprint(b.writer, "\r\n")
				if trimmed := strings.TrimSpace(line); trimmed != "" {
					b.history.Add(trimmed)
				}
				return line, nil

			case 0x03: // Ctrl+C — route through the interrupt counter
				now := time.Now()
				var force bool
				if b.onInterrupt != nil {
					// Delegate to the app-level handler (shares the same
					// counter as mid-turn Ctrl+C and emits its own notice).
					force = b.onInterrupt()
					fmt.Fprint(b.writer, "\r\n")
					if force {
						return "", io.EOF
					}
				} else {
					force = b.recordInterrupt(now)
					if force {
						fmt.Fprint(b.writer, "\r\n")
						return "", io.EOF
					}
					// Single interrupt at the prompt: cancel/no-op + re-prompt.
					fmt.Fprint(b.writer, "\r\n[Interrupted. You can give new instructions.]\r\n")
				}
				// Fall through: redrawLine below reprints the prompt + buffer.

			case 0x04: // Ctrl+D
				if len(buf) == 0 {
					fmt.Fprint(b.writer, "\r\n")
					return "", io.EOF
				}
				// On a non-empty line, treat as delete under cursor.
				if cursor < len(buf) {
					buf = append(buf[:cursor], buf[cursor+1:]...)
				}

			case 0x01: // Ctrl+A — beginning of line
				cursor = 0

			case 0x05: // Ctrl+E — end of line
				cursor = len(buf)

			case 0x0b: // Ctrl+K — kill to end of line
				buf = buf[:cursor]

			case 0x17: // Ctrl+W — delete word before cursor
				buf, cursor = deleteWordBefore(buf, cursor)

			case 0x08, 0x7f: // Backspace / Delete
				if cursor > 0 {
					buf = append(buf[:cursor-1], buf[cursor+1:]...)
					cursor--
				}

			case '\t': // Tab — file path completion
				b.doPathCompletion(&buf, &cursor)

			default:
				// Printable character (including multi-byte UTF-8).
				if key >= 0x20 && key != 0x7f {
					buf = append(buf, 0)
					copy(buf[cursor+1:], buf[cursor:])
					buf[cursor] = key
					cursor++
				}
			}
		}

		b.redrawLine(buf, cursor)
	}
}

// readTermKey reads a single keypress from the terminal in raw mode. It
// returns the key rune, an escape-sequence string (e.g. "[A" for Up), and a
// flag indicating whether this was an escape sequence.
func (b *InputBuffer) readTermKey() (rune, string, bool, error) {
	var first [1]byte
	_, err := b.tty.Read(first[:])
	if err != nil {
		return 0, "", false, err
	}

	if first[0] != 0x1b {
		return rune(first[0]), "", false, nil
	}

	// Escape received — try to read the escape sequence. The sequence bytes
	// arrive together, so a short timeout distinguishes a lone Esc from a
	// multi-byte sequence.
	seq, err := readTimeout(b.tty, 50*time.Millisecond)
	if err != nil || len(seq) == 0 {
		// Timeout — user pressed Escape alone.
		return 0x1b, "", false, nil
	}

	// Arrow / function keys: ESC [ <char>  or  ESC O <char>
	if seq[0] == '[' || seq[0] == 'O' {
		return 0, string(seq), true, nil
	}

	// Alt+key sequence: ESC <char>
	return 0, string(seq[:1]), true, nil
}

// readTimeout reads from f with a deadline. It returns any bytes that arrived
// before the deadline, or an empty slice on timeout.
//
// Preferred path: when f supports a read deadline (TTYs on macOS/Linux,
// sockets, pipes), the blocking Read returns on its own when the deadline
// elapses -- no helper goroutine is needed, so nothing leaks or steals the
// next keypress. Descriptors without deadline support (notably Windows console
// handles) fall back to a goroutine raced against time.After; the buffered
// result channel guarantees that helper exits as soon as its (eventual) read
// returns instead of blocking on a send the caller has already abandoned.
func readTimeout(f *os.File, timeout time.Duration) ([]byte, error) {
	buf := make([]byte, 8)

	if timeout > 0 {
		if err := f.SetReadDeadline(time.Now().Add(timeout)); err == nil {
			// The poller enforces the deadline: Read returns by itself when it
			// elapses. Clear it afterwards so later direct reads stay blocking.
			defer func() { _ = f.SetReadDeadline(time.Time{}) }()
			n, err := f.Read(buf)
			if err != nil && isReadTimeout(err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return buf[:n], nil
		}
	}

	// Fallback for descriptors without deadline support.
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := f.Read(buf)
		// Buffered (cap 1): the send never blocks, so once the read returns
		// the goroutine exits even after the caller has timed out.
		ch <- result{n, err}
	}()
	select {
	case <-time.After(timeout):
		return nil, nil
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return buf[:r.n], nil
	}
}

// isReadTimeout reports whether err is a read-deadline/timeout error raised by
// the poller (os.ErrDeadlineExceeded) or by the net layer (Timeout() == true).
func isReadTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) {
		return timeoutErr.Timeout()
	}
	return false
}

// redrawLine clears the current input line and reprints the buffer with the
// cursor at the correct position.
func (b *InputBuffer) redrawLine(buf []rune, cursor int) {
	// \r moves to column 0. \x1b[0K clears from cursor to end of line.
	fmt.Fprintf(b.writer, "\r\x1b[0K%s%s", b.prompt, string(buf))
	// Reposition the cursor.
	if len(buf) > cursor {
		fmt.Fprintf(b.writer, "\x1b[%dD", len(buf)-cursor)
	}
}

// doPathCompletion performs file / directory name completion for the word
// before the cursor.
func (b *InputBuffer) doPathCompletion(buf *[]rune, cursor *int) {
	line := string(*buf)
	if *cursor == 0 {
		return
	}

	// Find the start of the word before the cursor.
	prefix := line[:*cursor]
	wordStart := strings.LastIndexAny(prefix, " \t\n\r")
	if wordStart == -1 {
		wordStart = 0
	} else {
		wordStart++ // skip the separator
	}
	word := line[wordStart:*cursor]
	if word == "" {
		return
	}

	// List directory entries from the configured workspace. When no workspace
	// was wired in (workspaceDir == ""), fall back to the process CWD so the
	// behaviour is identical to the original os.ReadDir(".") path -- in
	// particular when CWD == workspace the candidate set is unchanged.
	listDir := b.workspaceDir
	if listDir == "" {
		listDir = "."
	}
	entries, err := os.ReadDir(listDir)
	if err != nil {
		return
	}

	// Build candidates: names that have the word as prefix, case-insensitive.
	candidates := make([]completionCandidate, 0)
	lowerWord := strings.ToLower(word)

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(word, ".") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(name), lowerWord) {
			candidates = append(candidates, completionCandidate{
				name:  name,
				isDir: entry.IsDir(),
			})
		}
	}

	if len(candidates) == 0 {
		return
	}

	// Sort: directories first, then alphabetical.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].isDir != candidates[j].isDir {
			return candidates[i].isDir
		}
		return strings.ToLower(candidates[i].name) < strings.ToLower(candidates[j].name)
	})

	if len(candidates) == 1 {
		// Single candidate — complete it.
		suffix := candidates[0].name[len(word):]
		if candidates[0].isDir {
			suffix += "/"
		}
		insertString(buf, cursor, suffix)
		return
	}

	// Multiple candidates — find the longest common prefix.
	lcp := longestCommonPrefixStr(word, candidates)
	if len(lcp) > len(word) {
		insertString(buf, cursor, lcp[len(word):])
		return
	}

	// No further common prefix — display choices below.
	fmt.Fprintf(b.writer, "\r\n")
	cols := b.terminalWidth()
	if cols <= 0 {
		cols = 80
	}
	printColumns(b.writer, candidates, cols, 2)
	// Reprint the prompt and current line.
	fmt.Fprint(b.writer, b.prompt)
	fmt.Fprint(b.writer, string(*buf))
}

// insertString inserts s into the buffer at cursor position and advances the
// cursor.
func insertString(buf *[]rune, cursor *int, s string) {
	if s == "" {
		return
	}
	insert := []rune(s)
	newBuf := make([]rune, len(*buf)+len(insert))
	copy(newBuf, (*buf)[:*cursor])
	copy(newBuf[*cursor:], insert)
	copy(newBuf[*cursor+len(insert):], (*buf)[*cursor:])
	*buf = newBuf
	*cursor += len(insert)
}

// deleteWordBefore deletes the word immediately before the cursor.
func deleteWordBefore(buf []rune, cursor int) ([]rune, int) {
	if cursor == 0 {
		return buf, 0
	}
	end := cursor
	for end > 0 && buf[end-1] == ' ' {
		end--
	}
	start := end
	for start > 0 && buf[start-1] != ' ' {
		start--
	}
	return append(buf[:start], buf[cursor:]...), start
}

// longestCommonPrefixStr returns the longest common prefix among the candidate
// names, starting from the given word.
func longestCommonPrefixStr(word string, candidates []completionCandidate) string {
	if len(candidates) == 0 {
		return word
	}
	common := candidates[0].name
	for _, c := range candidates[1:] {
		common = commonPrefix(common, c.name)
	}
	return common
}

func commonPrefix(a, b string) string {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:minLen]
}

// printColumns prints candidate names in aligned columns.
func printColumns(w io.Writer, candidates []completionCandidate, termWidth int, gutter int) {
	if len(candidates) == 0 {
		return
	}

	names := make([]string, len(candidates))
	maxLen := 0
	for i, c := range candidates {
		display := c.name
		if c.isDir {
			display += "/"
		}
		names[i] = display
		if len(display) > maxLen {
			maxLen = len(display)
		}
	}

	colWidth := maxLen + gutter
	nCols := termWidth / colWidth
	if nCols < 1 {
		nCols = 1
	}

	for i, name := range names {
		if i > 0 && i%nCols == 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%-*s", colWidth, name)
	}
	fmt.Fprintln(w)
}

// terminalWidth returns the current terminal width, or 0 if it cannot be
// determined.
func (b *InputBuffer) terminalWidth() int {
	if b.tty == nil {
		return 0
	}
	width, _, err := term.GetSize(int(b.tty.Fd()))
	if err != nil {
		return 0
	}
	return width
}
