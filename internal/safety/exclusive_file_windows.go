package safety

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

func validExclusivePath(path string) bool {
	volume := filepath.VolumeName(path)
	return filepath.IsAbs(path) && !strings.HasPrefix(path, `\\`) && utf8.ValidString(path) && !strings.Contains(path[len(volume):], ":") && strings.IndexFunc(path, unicode.IsControl) < 0
}

// OpenExclusiveFile requires the caller's approved root and pinned parents.
// Existing regular files cannot be edited, deleted or replaced by another handle.
func OpenExclusiveFile(path, operation string) (*os.File, error) {
	if !validExclusivePath(path) {
		return nil, errors.New("exclusive file requires an absolute local path")
	}
	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
	disposition := uint32(windows.OPEN_EXISTING)
	switch operation {
	case "edit":
	case "create":
		disposition = windows.CREATE_NEW
	case "delete":
		access = windows.GENERIC_READ | windows.DELETE
	case "rename":
		access = windows.GENERIC_READ | windows.GENERIC_WRITE | windows.DELETE
	default:
		return nil, errors.New("unsupported exclusive file operation")
	}
	encoded, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(encoded, access, 0, nil, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(handle)
		if operation == "create" {
			return nil, ErrExclusiveOutcomeUnknown
		}
		return nil, errors.New("exclusive file requires one regular file without aliases")
	}
	return os.NewFile(uintptr(handle), path), nil
}

func DeleteExclusiveFile(file *os.File) error {
	var remove byte = 1
	return windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileDispositionInfo, &remove, 1)
}

func RenameExclusiveFile(file *os.File, destination string) error {
	if !validExclusivePath(destination) {
		return errors.New("rename requires an absolute local target")
	}
	name, err := windows.UTF16FromString(destination)
	if err != nil {
		return err
	}
	name = name[:len(name)-1]
	var header struct {
		Replace uint32
		Root    windows.Handle
		Length  uint32
		Name    [1]uint16
	}
	length := int(unsafe.Offsetof(header.Name)) + len(name)*2
	buffer := make([]byte, length)
	data := (*struct {
		Replace uint32
		Root    windows.Handle
		Length  uint32
		Name    [1]uint16
	})(unsafe.Pointer(&buffer[0]))
	data.Length = uint32(len(name) * 2)
	copy(unsafe.Slice(&data.Name[0], len(name)), name)
	return windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileRenameInfo, &buffer[0], uint32(len(buffer)))
}
