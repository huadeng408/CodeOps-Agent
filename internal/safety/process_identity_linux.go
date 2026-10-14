//go:build linux

package safety

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func localProcessHost() (string, error) {
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil || strings.TrimSpace(string(machine)) == "" {
		return "", errors.New("machine identity unavailable")
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "", err
	}
	confirmedView := false
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "NSpid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != strconv.Itoa(os.Getpid()) {
			return "", errors.New("process namespace visibility cannot be confirmed")
		}
		confirmedView = true
	}
	if !confirmedView {
		return "", errors.New("process namespace visibility cannot be confirmed")
	}
	self, err := os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(os.Getpid()) {
		return "", errors.New("process namespace visibility cannot be confirmed")
	}
	namespace, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return "", err
	}
	userNamespace, err := os.Readlink("/proc/self/ns/user")
	if err != nil {
		return "", err
	}
	return "linux\x00" + strings.TrimSpace(string(machine)) + "\x00" + strconv.Itoa(os.Getuid()) + "\x00" + namespace + "\x00" + userNamespace, nil
}

func inspectScopeProcess(pid int) (string, bool, error) {
	if pid <= 0 || pid > 1<<31-1 {
		return "", false, errors.New("invalid process identity")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "", false, errors.New("process boot identity unavailable")
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return "", true, nil
		}
		return "", false, errors.New("process visibility cannot confirm stop")
	}
	if err != nil {
		return "", false, err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", false, errors.New("invalid process identity")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return "", false, errors.New("invalid process identity")
	}
	if start, err := strconv.ParseUint(fields[19], 10, 64); err != nil || start == 0 {
		return "", false, errors.New("invalid process start identity")
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], fields[0] == "Z" || fields[0] == "X", nil
}
