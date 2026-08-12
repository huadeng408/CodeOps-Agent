package cli

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestDetectTerminalCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		terminal bool
		width    int
		sizeErr  error
		unicode  bool
		want     TerminalCapabilities
	}{
		{
			name:     "interactive unicode terminal",
			terminal: true,
			width:    120,
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: true,
				Color:       true,
				Unicode:     true,
				Width:       120,
			},
		},
		{
			name:     "NO_COLOR disables color",
			env:      map[string]string{"NO_COLOR": "1"},
			terminal: true,
			width:    100,
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: true,
				Color:       false,
				Unicode:     true,
				Width:       100,
			},
		},
		{
			name:     "NO_COLOR wins over FORCE_COLOR",
			env:      map[string]string{"NO_COLOR": "1", "FORCE_COLOR": "1"},
			terminal: true,
			width:    100,
			unicode:  true,
			want:     TerminalCapabilities{Interactive: true, Color: false, Unicode: true, Width: 100},
		},
		{
			name:     "CLICOLOR zero disables color",
			env:      map[string]string{"CLICOLOR": "0"},
			terminal: true,
			width:    90,
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: true,
				Color:       false,
				Unicode:     true,
				Width:       90,
			},
		},
		{
			name:     "CLICOLOR zero wins over FORCE_COLOR",
			env:      map[string]string{"CLICOLOR": "0", "FORCE_COLOR": "1"},
			terminal: true,
			width:    100,
			unicode:  true,
			want:     TerminalCapabilities{Interactive: true, Color: false, Unicode: true, Width: 100},
		},
		{
			name:     "FORCE_COLOR only enables color",
			env:      map[string]string{"FORCE_COLOR": "1"},
			terminal: false,
			width:    132,
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: false,
				Color:       true,
				Unicode:     false,
				Width:       80,
			},
		},
		{
			name:     "CODE_AGENT_ASCII disables unicode",
			env:      map[string]string{"CODE_AGENT_ASCII": "1"},
			terminal: true,
			width:    88,
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: true,
				Color:       true,
				Unicode:     false,
				Width:       88,
			},
		},
		{
			name:     "non TTY uses safe fallback",
			terminal: false,
			width:    132,
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: false,
				Color:       false,
				Unicode:     false,
				Width:       80,
			},
		},
		{
			name:     "terminal width failure falls back",
			terminal: true,
			sizeErr:  errors.New("size unavailable"),
			unicode:  true,
			want: TerminalCapabilities{
				Interactive: true,
				Color:       true,
				Unicode:     true,
				Width:       80,
			},
		},
		{
			name:     "nonpositive terminal width falls back",
			terminal: true,
			width:    0,
			unicode:  true,
			want:     TerminalCapabilities{Interactive: true, Color: true, Unicode: true, Width: 80},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := capabilityEnv{
				getenv: func(key string) string {
					return tt.env[key]
				},
				isTerminal: func(int) bool {
					return tt.terminal
				},
				getSize: func(int) (int, int, error) {
					return tt.width, 24, tt.sizeErr
				},
				supportsUnicode: func() bool {
					return tt.unicode
				},
			}

			got := detectTerminalCapabilities(os.Stdout, env)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("detectTerminalCapabilities() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSupportsUnicodeFromLocaleUsesPrecedence(t *testing.T) {
	tests := []struct {
		name             string
		all, ctype, lang string
		want             bool
	}{
		{name: "LC_ALL wins", all: "C", ctype: "en_US.UTF-8", lang: "C", want: false},
		{name: "LC_CTYPE wins", all: "", ctype: "C", lang: "en_US.UTF-8", want: false},
		{name: "LANG fallback", all: "", ctype: "", lang: "en_US.UTF-8", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := supportsUnicodeFromLocale(tt.all, tt.ctype, tt.lang)
			if got != tt.want {
				t.Fatalf("supportsUnicodeFromLocale() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSupportsUnicodeFromWindowsOutputCodePage(t *testing.T) {
	if got := supportsUnicodeFromWindowsOutputCodePage(func() (uint32, error) { return 65001, nil }); !got {
		t.Fatal("UTF-8 console output code page should support Unicode")
	}
	if got := supportsUnicodeFromWindowsOutputCodePage(func() (uint32, error) { return 437, nil }); got {
		t.Fatal("non-UTF-8 console output code page should not support Unicode")
	}
}
