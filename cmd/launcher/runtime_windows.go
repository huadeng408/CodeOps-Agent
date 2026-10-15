package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type productJob struct {
	handle windows.Handle
	lock   windows.Handle
}

func newProductJob(data string) (*productJob, error) {
	name, err := windows.UTF16PtrFromString(filepath.Join(data, "launcher.lock"))
	if err != nil {
		return nil, errors.New("invalid user data directory")
	}
	lock, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, errors.New("this user data directory is already open or unavailable")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err == nil {
		limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	}
	if err != nil {
		windows.CloseHandle(lock)
		if job != 0 {
			windows.CloseHandle(job)
		}
		return nil, errors.New("cannot establish the owned Windows process job")
	}
	return &productJob{handle: job, lock: lock}, nil
}

func (job *productJob) start(command *exec.Cmd, logfile *os.File) (*os.Process, error) {
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, errors.New("cannot allocate the Windows process attributes")
	}
	defer attributes.Delete()
	// PROC_THREAD_ATTRIBUTE_JOB_LIST binds ownership atomically with CreateProcess (Windows 10+).
	const jobListAttribute = 0x0002000D
	if err := attributes.Update(jobListAttribute, unsafe.Pointer(&job.handle), unsafe.Sizeof(job.handle)); err != nil {
		return nil, errors.New("Windows cannot create a child inside the owned process job")
	}
	var output windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(logfile.Fd()), windows.CurrentProcess(), &output, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, errors.New("cannot attach the owned child log")
	}
	defer windows.CloseHandle(output)
	nullName, _ := windows.UTF16PtrFromString("NUL")
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	input, err := windows.CreateFile(nullName, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &security, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, errors.New("cannot prepare the owned child input")
	}
	defer windows.CloseHandle(input)
	handles := []windows.Handle{input, output}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, errors.New("cannot restrict the owned child's inherited handles")
	}
	application, err := windows.UTF16PtrFromString(command.Path)
	if err != nil {
		return nil, errors.New("invalid bundled application path")
	}
	arguments, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		return nil, errors.New("invalid bundled application arguments")
	}
	directory, err := windows.UTF16PtrFromString(command.Dir)
	if err != nil {
		return nil, errors.New("invalid child working directory")
	}
	entries := append([]string(nil), command.Env...)
	sort.Slice(entries, func(i, j int) bool { return strings.ToUpper(entries[i]) < strings.ToUpper(entries[j]) })
	block := []uint16{}
	for _, entry := range entries {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, errors.New("invalid child process environment")
		}
		block = append(block, encoded...)
	}
	block = append(block, 0)
	info := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	info.Cb = uint32(unsafe.Sizeof(info))
	info.Flags = windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW
	info.ShowWindow = windows.SW_HIDE
	info.StdInput, info.StdOutput, info.StdErr = input, output, output
	created := windows.ProcessInformation{}
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	if err := windows.CreateProcess(application, arguments, nil, nil, true, flags, &block[0], directory, &info.StartupInfo, &created); err != nil {
		return nil, errors.New("cannot start the bundled child inside its Windows job")
	}
	defer windows.CloseHandle(created.Thread)
	defer windows.CloseHandle(created.Process)
	process, err := os.FindProcess(int(created.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(created.Process, 1)
		return nil, errors.New("cannot retain the owned child process handle")
	}
	return process, nil
}

func (job *productJob) close() {
	if job.handle != 0 {
		windows.CloseHandle(job.handle)
		job.handle = 0
	}
	if job.lock != 0 {
		windows.CloseHandle(job.lock)
		job.lock = 0
	}
}

func interruptChild(process *os.Process) {
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(process.Pid))
}

func openBrowser(url string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(url)
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
