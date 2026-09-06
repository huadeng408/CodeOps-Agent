//go:build windows

package sandbox

import (
	"os/exec"
	"strconv"
)

func configureSandboxProcess(_ *exec.Cmd) {}

func killSandboxProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/PID", pid, "/T", "/F").Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
