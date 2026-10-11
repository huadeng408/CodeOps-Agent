package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"code-agent/internal/safety"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	cmd            *exec.Cmd
	binary         string
	prefix         []string
	createArgs     []string
	containerID    string
	startOutput    string
	startTruncated bool
	mu             sync.Mutex
	removed        bool
}

func (p *commandProcess) StdoutPipe() (io.ReadCloser, error) { return p.cmd.StdoutPipe() }
func (p *commandProcess) StderrPipe() (io.ReadCloser, error) { return p.cmd.StderrPipe() }
func (p *commandProcess) StdinPipe() (io.WriteCloser, error) { return p.cmd.StdinPipe() }

var ErrSandboxOutcomeUnknown = errors.New("sandbox container outcome is unknown; reconciliation required")

const maxCapturedOutput = 1 << 20

func newContainerProcess(binary string, args []string, runIndex int, interactive bool) (*commandProcess, error) {
	if runIndex >= len(args) || args[runIndex] != "run" {
		return nil, errors.New("invalid sandbox container invocation")
	}
	name := "codeops-sandbox-" + strings.ToLower(rand.Text())
	prefix := append([]string(nil), args[:runIndex]...)
	create := append([]string{"create", "--name", name, "--label", "codeops.run=" + name, "--log-driver", "none"}, args[runIndex+1:]...)
	attach := append(append([]string(nil), prefix...), "start", "--attach")
	if interactive {
		attach = append(attach, "--interactive")
	}
	attach = append(attach, name)
	cmd := exec.Command(binary, attach...)
	cmd.Env = safety.ScrubEnvironment(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	configureSandboxProcess(cmd)
	return &commandProcess{cmd: cmd, binary: binary, prefix: prefix, createArgs: create}, nil
}

func (p *commandProcess) output(ctx context.Context, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, p.binary, append(append([]string(nil), p.prefix...), args...)...)
	cmd.Env = safety.ScrubEnvironment(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	buffer := &boundedBuffer{}
	cmd.Stdout, cmd.Stderr = buffer, buffer
	err := cmd.Run()
	return buffer.String(), buffer.truncated, err
}

func (p *commandProcess) Start() error { return p.start(context.Background()) }

func (p *commandProcess) start(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, truncated, err := p.output(ctx, p.createArgs...)
	p.startOutput, p.startTruncated = output, truncated
	if err != nil {
		if ctx.Err() == nil && strings.HasPrefix(strings.TrimSpace(output), "Error response from daemon: No such image: ") {
			return err
		}
		return fmt.Errorf("%w: container creation unconfirmed (%s)", ErrSandboxOutcomeUnknown, p.createArgs[2])
	}
	containerID := strings.TrimSpace(output)
	decoded, decodeErr := hex.DecodeString(containerID)
	if decodeErr != nil || len(decoded) != 32 {
		return fmt.Errorf("%w: container creation returned no exact ID (%s)", ErrSandboxOutcomeUnknown, p.createArgs[2])
	}
	p.containerID = containerID
	p.cmd.Args[len(p.cmd.Args)-1] = containerID
	p.startOutput = ""
	if err := p.cmd.Start(); err != nil {
		return errors.Join(err, p.remove())
	}
	return nil
}

// remove only targets the exact ID returned by this process's Docker create.
// Stopping the attached CLI alone would leave the daemon-owned task running.
func (p *commandProcess) remove() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.removed {
		return nil
	}
	if p.containerID == "" {
		return ErrSandboxOutcomeUnknown
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, _, err := p.output(ctx, "rm", "--force", p.containerID)
	if err != nil && !strings.Contains(output, "No such container: "+p.containerID) {
		return fmt.Errorf("%w: cannot stop owned container %s", ErrSandboxOutcomeUnknown, p.containerID)
	}
	// Docker rm succeeds only after removal; a precise not-found also confirms
	// the --rm container has already exited. Daemon/proxy failures remain unknown.
	p.removed = true
	return nil
}

func (p *commandProcess) Wait() error { return errors.Join(p.cmd.Wait(), p.remove()) }

func (p *commandProcess) Kill() error {
	err := p.remove()
	_ = killSandboxProcessTree(p.cmd)
	return err
}

func runContainer(ctx context.Context, binary string, args []string, runIndex int, interactive bool) (result Result, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Tracer("code-agent/sandbox").Start(ctx, "sandbox.run")
	defer func() {
		span.SetAttributes(attribute.Int("sandbox.exit_code", result.ExitCode), attribute.Bool("sandbox.output_truncated", result.Truncated), attribute.Bool("sandbox.outcome_unknown", errors.Is(err, ErrSandboxOutcomeUnknown)))
		if err != nil {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}()
	if err = ctx.Err(); err != nil {
		return Result{ExitCode: 124}, err
	}
	p, err := newContainerProcess(binary, args, runIndex, interactive)
	if err != nil {
		return Result{ExitCode: 126}, err
	}
	buffer := &boundedBuffer{}
	p.cmd.Stdout, p.cmd.Stderr = buffer, buffer
	if err := p.start(ctx); err != nil {
		code := 125 // Docker setup failed before the task executable ran.
		if errors.Is(err, ErrSandboxOutcomeUnknown) {
			code = 126
		}
		return Result{Output: p.startOutput, ExitCode: code, Truncated: p.startTruncated}, err
	}
	span.SetAttributes(attribute.String("sandbox.container_id", p.containerID))
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		killErr := p.Kill()
		err = errors.Join(ctx.Err(), killErr, <-done)
	}
	result = Result{Output: buffer.String(), Truncated: buffer.truncated}
	if err != nil {
		result.ExitCode = exitCode(err)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			result.ExitCode = 124
		}
		if errors.Is(err, ErrSandboxOutcomeUnknown) {
			result.ExitCode = 126
		}
	}
	return result, err
}

type boundedBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	if len(b.data)+n <= maxCapturedOutput {
		b.data = append(b.data, data...)
		return n, nil
	}
	marker := []byte("\n[output truncated]\n")
	head := (maxCapturedOutput - len(marker)) / 2
	tail := maxCapturedOutput - len(marker) - head
	prefix := make([]byte, 0, maxCapturedOutput)
	if len(b.data) < head {
		prefix = append(prefix, b.data...)
		prefix = append(prefix, data[:head-len(b.data)]...)
	} else {
		prefix = append(prefix, b.data[:head]...)
	}
	prefix = append(prefix, marker...)
	if n < tail {
		prefix = append(prefix, b.data[len(b.data)-(tail-n):]...)
	}
	if n > tail {
		data = data[n-tail:]
	}
	b.data = append(prefix, data...)
	b.truncated = true
	return n, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
