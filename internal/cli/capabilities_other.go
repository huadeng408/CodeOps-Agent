//go:build !windows

package cli

import "os"

func supportsUnicodePlatform() bool {
	return supportsUnicodeFromLocale(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
}
