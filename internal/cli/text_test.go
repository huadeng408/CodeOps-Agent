package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestCellWidthIgnoresANSIAndMeasuresCJKCells(t *testing.T) {
	if got := cellWidth("\u4e2da"); got != 3 {
		t.Fatalf("cellWidth(CJK text) = %d, want 3", got)
	}
	if got := cellWidth("\x1b[36m\u4e2da\x1b[0m"); got != 3 {
		t.Fatalf("cellWidth(ANSI text) = %d, want 3", got)
	}
}

func TestSanitizeTerminalTextNeutralizesControlsAndCSI(t *testing.T) {
	input := "safe\x00\x1b[2J\x1b[31m red\x1b[0m\r\n\tindent\x7f"
	want := "safe red\n\tindent"
	if got := sanitizeTerminalText(input); got != want {
		t.Fatalf("sanitizeTerminalText() = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTextNeutralizesC1CSI(t *testing.T) {
	input := "safe\x9b31m red\x9b0m"
	if got, want := sanitizeTerminalText(input), "safe red"; got != want {
		t.Fatalf("sanitizeTerminalText() = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTextRemovesOSCPayload(t *testing.T) {
	input := "before\x1b]8;;https://example.test\x1b\\link\x1b]8;;\x1b\\after"
	if got, want := sanitizeTerminalText(input), "beforelinkafter"; got != want {
		t.Fatalf("sanitizeTerminalText(OSC) = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTextRemovesDCSPayload(t *testing.T) {
	input := "before\x1bP1;2|untrusted payload\x1b\\after"
	if got, want := sanitizeTerminalText(input), "beforeafter"; got != want {
		t.Fatalf("sanitizeTerminalText(DCS) = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTextRemovesUTF8C1CSI(t *testing.T) {
	input := "before\u009b31mred\u009b0mafter"
	if got, want := sanitizeTerminalText(input), "beforeredafter"; got != want {
		t.Fatalf("sanitizeTerminalText(UTF-8 C1 CSI) = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTextPreservesNewlinesAndTabsOutsideANSI(t *testing.T) {
	input := "\x1b[32mfirst\n\tsecond\x1b[0m"
	if got, want := sanitizeTerminalText(input), "first\n\tsecond"; got != want {
		t.Fatalf("sanitizeTerminalText() = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTextDropsNewlinesAndTabsInsideStringControls(t *testing.T) {
	input := "first\n\x1b]payload\n\tignored\x1b\\second"
	if got, want := sanitizeTerminalText(input), "first\nsecond"; got != want {
		t.Fatalf("sanitizeTerminalText() = %q, want %q", got, want)
	}
}

func TestWrapCellsPreservesGraphemeClusters(t *testing.T) {
	input := "A\U0001f469\u200d\U0001f4bb\U0001f1e8\U0001f1f3B"
	want := []string{"A", "\U0001f469\u200d\U0001f4bb", "\U0001f1e8\U0001f1f3B"}
	if got := wrapCells(input, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapCells() = %#v, want %#v", got, want)
	}
}

func TestWrapCellsReplacesWideGraphemeAtNarrowWidth(t *testing.T) {
	want := []string{"?", "a"}
	if got := wrapCells("\u4e2da", 1); !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapCells() = %#v, want %#v", got, want)
	}
	for _, line := range wrapCells("\u4e2d\U0001f469\u200d\U0001f4bb", 1) {
		if got := cellWidth(line); got > 1 {
			t.Fatalf("wrapped line %q has width %d, want <= 1", line, got)
		}
	}
}

func TestSanitizeTerminalTextRemovesBidirectionalOverrides(t *testing.T) {
	input := "safe\u202eevil\u202c and \u2066isolated\u2069"
	want := "safeevil and isolated"
	if got := sanitizeTerminalText(input); got != want {
		t.Fatalf("sanitizeTerminalText(bidi) = %q, want %q", got, want)
	}
	if got := sanitizeTerminalText("A\U0001f469\u200d\U0001f4bb\ufe0fB"); !strings.Contains(got, "\u200d") || !strings.Contains(got, "\ufe0f") {
		t.Fatalf("grapheme format characters were removed: %q", got)
	}
}

func TestWrapCellsHardWrapsTokensAndStaysWithinWidth(t *testing.T) {
	lines := wrapCells("alpha\t\u4e2d\u6587abcdef\nend", 5)
	if got, want := strings.Join(lines, "|"), "alpha|   \u4e2d|\u6587abc|def|end"; got != want {
		t.Fatalf("wrapCells() = %q, want %q", got, want)
	}
	for _, line := range lines {
		if got := cellWidth(line); got > 5 {
			t.Fatalf("wrapped line %q has width %d, want <= 5", line, got)
		}
	}
}

func TestWrapCellsHandlesNarrowWidthAndSanitizesContent(t *testing.T) {
	lines := wrapCells("\x1b[2Kab", 1)
	if got, want := strings.Join(lines, "|"), "a|b"; got != want {
		t.Fatalf("wrapCells() = %q, want %q", got, want)
	}
	for _, line := range lines {
		if got := cellWidth(line); got > 1 {
			t.Fatalf("wrapped line %q has width %d, want <= 1", line, got)
		}
	}
}

func TestTruncateAndPadCellsUseDisplayWidth(t *testing.T) {
	if got, want := truncateCells("ab\u4e2d\u6587", 5), "ab\u4e2d"; got != want {
		t.Fatalf("truncateCells() = %q, want %q", got, want)
	}
	if got, want := padCells("\u4e2da", 5), "\u4e2da  "; got != want {
		t.Fatalf("padCells() = %q, want %q", got, want)
	}
}
