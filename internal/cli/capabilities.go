package cli

import (
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

const fallbackTerminalWidth = 80

// TerminalCapabilities describes terminal features that are safe to use.
type TerminalCapabilities struct {
	Interactive bool
	Color       bool
	Unicode     bool
	Width       int
}

type capabilityEnv struct {
	getenv          func(string) string
	isTerminal      func(int) bool
	getSize         func(int) (int, int, error)
	supportsUnicode func() bool
}

func detectTerminalCapabilities(out io.Writer, env capabilityEnv) TerminalCapabilities {
	capabilities := TerminalCapabilities{Width: fallbackTerminalWidth}

	fdWriter, ok := out.(interface{ Fd() uintptr })
	if ok && env.isTerminal != nil {
		fd := int(fdWriter.Fd())
		capabilities.Interactive = env.isTerminal(fd)
		if capabilities.Interactive {
			capabilities.Color = true
			if env.supportsUnicode != nil {
				capabilities.Unicode = env.supportsUnicode()
			}
			if env.getSize != nil {
				if width, _, err := env.getSize(fd); err == nil && width > 0 {
					capabilities.Width = width
				}
			}
		}
	}

	getenv := func(string) string { return "" }
	if env.getenv != nil {
		getenv = env.getenv
	}
	if getenv("NO_COLOR") != "" || getenv("CLICOLOR") == "0" {
		capabilities.Color = false
	} else if getenv("FORCE_COLOR") != "" {
		capabilities.Color = true
	}
	if getenv("CODE_AGENT_ASCII") == "1" {
		capabilities.Unicode = false
	}

	return capabilities
}

func defaultCapabilityEnv() capabilityEnv {
	return capabilityEnv{
		getenv:          os.Getenv,
		isTerminal:      term.IsTerminal,
		getSize:         term.GetSize,
		supportsUnicode: supportsUnicodeTerminal,
	}
}

func supportsUnicodeTerminal() bool {
	return supportsUnicodePlatform()
}

func supportsUnicodeFromLocale(all, ctype, lang string) bool {
	locale := all
	if locale == "" {
		locale = ctype
	}
	if locale == "" {
		locale = lang
	}
	locale = strings.ToLower(locale)
	return strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8")
}

func supportsUnicodeFromWindowsOutputCodePage(getCodePage func() (uint32, error)) bool {
	codePage, err := getCodePage()
	return err == nil && codePage == 65001
}
