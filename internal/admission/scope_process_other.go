//go:build !windows && !linux

package admission

import "errors"

func localProcessHost() (string, error) {
	return "", errors.New("scope process identity is unsupported")
}

func inspectScopeProcess(_ int) (string, bool, error) {
	return "", false, errors.New("scope process identity is unsupported")
}
