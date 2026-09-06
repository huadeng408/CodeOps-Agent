package sandbox

import (
	"context"
	"io"
	"os/exec"
)

// Process is the narrow child-process contract used by streaming jobs. The
// sandbox owns process creation; callers only receive pipes and lifecycle
// operations, never a host command that can bypass isolation.
type Process interface {
	StdoutPipe() (io.ReadCloser, error)
	StderrPipe() (io.ReadCloser, error)
	StdinPipe() (io.WriteCloser, error)
	Start() error
	Wait() error
	Kill() error
}

// StreamingRunner starts an isolated process while preserving live pipes for
// background jobs. Implementations must reject an unavailable backend before
// returning a process and must not bind process lifetime to ctx cancellation.
type StreamingRunner interface {
	Start(context.Context, Request) (Process, error)
}

type commandProcess struct {
	cmd *exec.Cmd
}

func (p *commandProcess) StdoutPipe() (io.ReadCloser, error) { return p.cmd.StdoutPipe() }
func (p *commandProcess) StderrPipe() (io.ReadCloser, error) { return p.cmd.StderrPipe() }
func (p *commandProcess) StdinPipe() (io.WriteCloser, error) { return p.cmd.StdinPipe() }
func (p *commandProcess) Start() error                       { return p.cmd.Start() }
func (p *commandProcess) Wait() error                        { return p.cmd.Wait() }
func (p *commandProcess) Kill() error                        { return killSandboxProcessTree(p.cmd) }
