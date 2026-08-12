package cli

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"
)

// cellWidth returns the terminal cell width of printable text. ANSI escapes do
// not occupy terminal cells and are excluded before measurement.
func cellWidth(text string) int {
	return runewidth.StringWidth(stripANSI(text))
}

// stripANSI removes ECMA-48 escape sequences. This includes CSI controls and
// string controls (OSC, DCS, SOS, PM, and APC) with either ST terminator.
func stripANSI(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); {
		if next, escaped := terminalControlEnd(text, index); escaped {
			index = next
			continue
		}

		runeValue, size := utf8.DecodeRuneInString(text[index:])
		if runeValue == utf8.RuneError && size == 1 {
			// Invalid UTF-8 cannot safely be printed as terminal content.
			index++
			continue
		}
		out.WriteRune(runeValue)
		index += size
	}
	return out.String()
}

// sanitizeTerminalText permits only printable content plus tab and newline.
// It discards terminal controls so untrusted content cannot manipulate output.
func sanitizeTerminalText(text string) string {
	text = stripANSI(text)
	var out strings.Builder
	for _, runeValue := range text {
		switch runeValue {
		case '\r':
			continue
		case '\n', '\t':
			out.WriteRune(runeValue)
		default:
			if !unicode.IsControl(runeValue) && !isDangerousDirectionalControl(runeValue) {
				out.WriteRune(runeValue)
			}
		}
	}
	return out.String()
}

// wrapCells sanitizes content and wraps it to a maximum terminal cell width.
// Tabs advance to the next four-cell tab stop and therefore become spaces. A
// grapheme wider than width is represented by a one-cell marker so the
// width invariant remains true without silently dropping content.
func wrapCells(text string, width int) []string {
	if width < 1 {
		width = 1
	}

	text = sanitizeTerminalText(text)
	lines := make([]string, 0, strings.Count(text, "\n")+1)
	var line strings.Builder
	lineWidth := 0
	appendLine := func() {
		lines = append(lines, line.String())
		line.Reset()
		lineWidth = 0
	}
	lastWasNewline := false

	clusters := graphemes.FromString(text)
	for clusters.Next() {
		cluster := clusters.Value()
		switch cluster {
		case "\n":
			appendLine()
			lastWasNewline = true
			continue
		case "\t":
			spaces := 4 - (lineWidth % 4)
			for range spaces {
				if lineWidth == width {
					appendLine()
				}
				line.WriteByte(' ')
				lineWidth++
			}
			lastWasNewline = false
			continue
		}

		clusterWidth := runewidth.StringWidth(cluster)
		if clusterWidth > width {
			if line.Len() > 0 {
				appendLine()
			}
			lines = append(lines, "?")
			lastWasNewline = false
			continue
		}
		if clusterWidth > 0 && lineWidth+clusterWidth > width && line.Len() > 0 {
			appendLine()
		}
		line.WriteString(cluster)
		lineWidth += clusterWidth
		lastWasNewline = false
	}
	if line.Len() > 0 || len(lines) == 0 || lastWasNewline {
		appendLine()
	}
	return lines
}

func isDangerousDirectionalControl(runeValue rune) bool {
	switch runeValue {
	case '\u061c', '\u200e', '\u200f':
		return true
	}
	return runeValue >= '\u202a' && runeValue <= '\u202e' ||
		runeValue >= '\u2066' && runeValue <= '\u206f'
}

// truncateCells returns the longest sanitized grapheme prefix that fits within
// width. It never splits a user-perceived character.
func truncateCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	var out strings.Builder
	used := 0
	clusters := graphemes.FromString(sanitizeTerminalText(text))
	for clusters.Next() {
		cluster := clusters.Value()
		if cluster == "\n" || cluster == "\t" {
			break
		}
		clusterWidth := runewidth.StringWidth(cluster)
		if used+clusterWidth > width {
			break
		}
		out.WriteString(cluster)
		used += clusterWidth
	}
	return out.String()
}

// padCells pads sanitized, single-line text to the requested cell width.
func padCells(text string, width int) string {
	text = truncateCells(text, width)
	padding := width - cellWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func terminalControlEnd(text string, index int) (int, bool) {
	if text[index] == '\x1b' {
		if index+1 >= len(text) {
			return len(text), true
		}
		switch text[index+1] {
		case '[':
			return skipCSI(text, index+2), true
		case ']', 'P', 'X', '^', '_':
			return skipStringControl(text, index+2), true
		default:
			return index + 2, true
		}
	}

	if index < len(text) && text[index] >= 0x80 && text[index] <= 0x9f {
		return skipRawC1Control(text, index)
	}
	runeValue, size := utf8.DecodeRuneInString(text[index:])
	if size > 1 && runeValue >= 0x80 && runeValue <= 0x9f {
		switch runeValue {
		case 0x9b:
			return skipCSI(text, index+size), true
		case 0x9d, 0x90, 0x98, 0x9e, 0x9f:
			return skipStringControl(text, index+size), true
		default:
			return index + size, true
		}
	}
	return index, false
}

func skipRawC1Control(text string, index int) (int, bool) {
	switch text[index] {
	case 0x9b:
		return skipCSI(text, index+1), true
	case 0x9d, 0x90, 0x98, 0x9e, 0x9f:
		return skipStringControl(text, index+1), true
	default:
		return index + 1, true
	}
}

func skipCSI(text string, index int) int {
	for index < len(text) {
		byteValue := text[index]
		index++
		if byteValue >= 0x40 && byteValue <= 0x7e {
			return index
		}
	}
	return len(text)
}

func skipStringControl(text string, index int) int {
	for index < len(text) {
		if text[index] == '\a' {
			return index + 1
		}
		if text[index] == '\x1b' && index+1 < len(text) && text[index+1] == '\\' {
			return index + 2
		}
		if text[index] == 0x9c {
			return index + 1
		}
		runeValue, size := utf8.DecodeRuneInString(text[index:])
		if runeValue == 0x9c && size > 1 {
			return index + size
		}
		index += size
	}
	return len(text)
}
