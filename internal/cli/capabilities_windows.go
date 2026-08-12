//go:build windows

package cli

import "golang.org/x/sys/windows"

func supportsUnicodePlatform() bool {
	return supportsUnicodeFromWindowsOutputCodePage(windows.GetConsoleOutputCP)
}
