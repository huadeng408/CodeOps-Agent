//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

type productJob struct{}

func newProductJob(string) (*productJob, error) {
	return nil, errors.New("the portable product requires Windows 10/11 x64")
}
func (*productJob) start(*exec.Cmd, *os.File) (*os.Process, error) {
	return nil, errors.New("Windows is required")
}
func (*productJob) close()       {}
func interruptChild(*os.Process) {}
func openBrowser(string) error   { return errors.New("Windows is required") }
