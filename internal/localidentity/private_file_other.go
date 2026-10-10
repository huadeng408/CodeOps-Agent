//go:build !windows

package localidentity

import "code-agent/internal/safety"

func rejectReparsePoint(string) error { return nil }

func protectIdentityPath(path string) error {
	return safety.ProtectPrivatePath(path)
}
