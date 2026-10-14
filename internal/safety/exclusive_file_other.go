//go:build !windows

package safety

import (
	"errors"
	"os"
)

func OpenExclusiveFile(string, string) (*os.File, error) {
	return nil, errors.New("exclusive file lifecycle is only available on Windows")
}
func DeleteExclusiveFile(*os.File) error {
	return errors.New("exclusive file lifecycle is only available on Windows")
}
func RenameExclusiveFile(*os.File, string) error {
	return errors.New("exclusive file lifecycle is only available on Windows")
}
