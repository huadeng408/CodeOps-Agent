//go:build windows

package admission

import (
	"errors"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func localProcessHost() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	machine, _, err := key.GetStringValue("MachineGuid")
	if err != nil || strings.TrimSpace(machine) == "" {
		return "", errors.New("machine identity unavailable")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return "windows\x00" + strings.TrimSpace(machine) + "\x00" + user.User.Sid.String(), nil
}

func inspectScopeProcess(pid int) (string, bool, error) {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return "", false, errors.New("invalid process identity")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", false, err
	}
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || (state != windows.WAIT_OBJECT_0 && state != uint32(windows.WAIT_TIMEOUT)) {
		return "", false, errors.New("process stop cannot be confirmed")
	}
	started := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	if started == 0 {
		return "", false, errors.New("process start identity unavailable")
	}
	return strconv.FormatUint(started, 10), state == windows.WAIT_OBJECT_0, nil
}
