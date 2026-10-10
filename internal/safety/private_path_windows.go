package safety

import (
	"errors"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
)

// PinDirectories prevents Windows directory replacement while
// native Git resolves its named paths. Child file creation remains permitted.
func PinDirectories(paths ...string) (func(), error) {
	return pinWindowsPaths(true, paths...)
}

func PinReadOnlyFiles(paths ...string) (func(), error) {
	return pinWindowsPaths(false, paths...)
}

func pinWindowsPaths(directories bool, paths ...string) (func(), error) {
	var handles []windows.Handle
	closeAll := func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}
	seen := map[string]bool{}
	for _, name := range paths {
		if !filepath.IsAbs(name) || strings.HasPrefix(name, `\\`) {
			closeAll()
			return nil, errors.New("directory pin requires an absolute local path")
		}
		volume := filepath.VolumeName(name) + `\`
		rel, err := filepath.Rel(volume, filepath.Clean(name))
		if err != nil {
			closeAll()
			return nil, err
		}
		parts := []string{volume}
		if rel != "." {
			for _, part := range strings.Split(rel, `\`) {
				parts = append(parts, filepath.Join(parts[len(parts)-1], part))
			}
		}
		for _, part := range parts {
			key := strings.ToLower(part)
			if seen[key] {
				continue
			}
			encoded, err := windows.UTF16PtrFromString(part)
			if err != nil {
				closeAll()
				return nil, err
			}
			isDirectory := directories || !strings.EqualFold(part, filepath.Clean(name))
			share := uint32(windows.FILE_SHARE_READ)
			if isDirectory {
				share |= windows.FILE_SHARE_WRITE
			}
			handle, err := windows.CreateFile(encoded, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				closeAll()
				return nil, errors.New("directory cannot be pinned")
			}
			handles = append(handles, handle)
			var info windows.ByHandleFileInformation
			if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != isDirectory {
				closeAll()
				return nil, errors.New("directory pin contains a reparse point or non-directory")
			}
			seen[key] = true
		}
	}
	return closeAll, nil
}

func RejectReparsePoint(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse point is not allowed")
	}
	return nil
}

// ProtectPrivatePath is used only for newly created Harness-owned artifacts.
func ProtectPrivatePath(path string) error {
	if err := RejectReparsePoint(path); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
