//go:build !windows

package sandbox

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureSandboxProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killSandboxProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	} else if !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return cmd.Process.Kill()
}
