package localidentity

import "code-agent/internal/safety"

func rejectReparsePoint(path string) error {
	return safety.RejectReparsePoint(path)
}

func protectIdentityPath(path string) error {
	return safety.ProtectPrivatePath(path)
}
