//go:build windows

package jobs

import "os/exec"

func configureProcess(_ *exec.Cmd) {}

func killProcessGroup(_ int) error {
	return nil
}
