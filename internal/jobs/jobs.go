// Package jobs owns the lifecycle of background repository processes.
//
// The registry is deliberately process-local: the Go Harness is the authority
// for starting, observing and stopping work. Callers receive immutable
// snapshots and never hold a live *exec.Cmd or mutable output buffer.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"code-agent/internal/safety"
)

// Status is the externally observable process lifecycle state.
type Status string

const (
	StatusRunning   Status = "running"
	StatusStopping  Status = "stopping"
	StatusCompleted Status = "completed"
	StatusKilled    Status = "killed"
	StatusFailed    Status = "failed"
)

// Config bounds resources retained by one registry.
type Config struct {
	MaxConcurrentPerOwner int
	MaxOutputBytes        int
}

const (
	defaultMaxConcurrentPerOwner = 10
	defaultMaxOutputBytes        = 1 << 20
)

// Spec describes one background process. Command is interpreted by the
// platform shell; Program and Args are the structured alternative.
type Spec struct {
	Kind             string
	Label            string
	Owner            string
	Command          string
	Program          string
	Args             []string
	WorkingDir       string
	OutputLimitBytes int
	Timeout          time.Duration
	Interactive      bool
}

// Snapshot is a read-only projection of a job's lifecycle.
type Snapshot struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Label           string    `json:"label"`
	Owner           string    `json:"owner,omitempty"`
	Status          Status    `json:"status"`
	Detail          string    `json:"detail,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	OutputBytes     int       `json:"output_bytes"`
	OutputTruncated bool      `json:"output_truncated"`
	ExitCode        int       `json:"exit_code"`
}

// ReadResult contains output since the previous Read call and the current
// snapshot. Output is intentionally incremental to keep model-facing payloads
// bounded for long-running jobs.
type ReadResult struct {
	Text     string
	Bytes    int
	Snapshot Snapshot
}

type trackedJob struct {
	spec       Spec
	id         string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdinMu    sync.Mutex
	done       chan struct{}
	output     []byte
	readOffset int
	status     Status
	detail     string
	exitCode   int
	truncated  bool
	startedAt  time.Time
	finishedAt time.Time
	timer      *time.Timer
}

// Registry tracks all jobs started by one Harness process.
type Registry struct {
	root                  string
	maxConcurrentPerOwner int
	maxOutputBytes        int

	mu     sync.Mutex
	jobs   map[string]*trackedJob
	next   map[string]int
	closed bool
}

// NewRegistry constructs a process-local registry. A blank root resolves to
// the current directory; callers should pass the Harness workspace root.
func NewRegistry(root string, config Config) *Registry {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		root = "."
	}
	if config.MaxConcurrentPerOwner <= 0 {
		config.MaxConcurrentPerOwner = defaultMaxConcurrentPerOwner
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = defaultMaxOutputBytes
	}
	return &Registry{
		root:                  root,
		maxConcurrentPerOwner: config.MaxConcurrentPerOwner,
		maxOutputBytes:        config.MaxOutputBytes,
		jobs:                  make(map[string]*trackedJob),
		next:                  make(map[string]int),
	}
}

// Start launches a detached background process. The parent context is used
// only to reject an already-cancelled request; cancellation of the request
// does not kill the job, which is what lets work survive a turn boundary.
func (r *Registry) Start(ctx context.Context, spec Spec) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	spec.Kind = strings.TrimSpace(spec.Kind)
	if spec.Kind == "" {
		spec.Kind = "process"
	}
	spec.Label = strings.TrimSpace(spec.Label)
	if spec.Label == "" {
		spec.Label = spec.Kind
	}
	command := strings.TrimSpace(spec.Command)
	program := strings.TrimSpace(spec.Program)
	if (command == "") == (program == "") {
		return Snapshot{}, errors.New("job spec must contain exactly one of command or program")
	}
	if spec.OutputLimitBytes <= 0 || spec.OutputLimitBytes > r.maxOutputBytes {
		spec.OutputLimitBytes = r.maxOutputBytes
	}
	if strings.TrimSpace(spec.WorkingDir) == "" {
		spec.WorkingDir = r.root
	} else {
		abs, err := filepath.Abs(spec.WorkingDir)
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve job working directory: %w", err)
		}
		spec.WorkingDir = abs
	}
	rootAbs, err := filepath.Abs(r.root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve job workspace: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, spec.WorkingDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Snapshot{}, fmt.Errorf("job working directory escapes workspace: %s", spec.WorkingDir)
	}
	if info, err := os.Stat(spec.WorkingDir); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("working directory is not a directory")
		}
		return Snapshot{}, fmt.Errorf("job working directory: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Snapshot{}, errors.New("job registry is closed")
	}
	if r.activeCountLocked(spec.Owner) >= r.maxConcurrentPerOwner {
		return Snapshot{}, fmt.Errorf("background job limit reached for owner %q (limit: %d)", spec.Owner, r.maxConcurrentPerOwner)
	}

	var cmd *exec.Cmd
	if program != "" {
		cmd = exec.Command(program, spec.Args...)
	} else {
		name, args := shellCommand(command)
		cmd = exec.Command(name, args...)
	}
	cmd.Dir = spec.WorkingDir
	cmd.Env = safety.ScrubEnvironment(os.Environ())
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Snapshot{}, fmt.Errorf("open job stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Snapshot{}, fmt.Errorf("open job stderr: %w", err)
	}
	var stdin io.WriteCloser
	if spec.Interactive {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return Snapshot{}, fmt.Errorf("open job stdin: %w", err)
		}
	}
	if err := cmd.Start(); err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		return Snapshot{}, fmt.Errorf("start background job: %w", err)
	}

	count := r.next[spec.Kind] + 1
	r.next[spec.Kind] = count
	id := fmt.Sprintf("%s-%d", spec.Kind, count)
	job := &trackedJob{
		spec:      spec,
		id:        id,
		cmd:       cmd,
		stdin:     stdin,
		done:      make(chan struct{}),
		status:    StatusRunning,
		startedAt: time.Now().UTC(),
	}
	r.jobs[id] = job
	go r.capture(job, stdout, stderr)
	if spec.Timeout > 0 {
		job.timer = time.AfterFunc(spec.Timeout, func() {
			_, _ = r.Kill(job.id, job.spec.Owner, "timeout")
		})
	}
	return r.snapshotLocked(job), nil
}

// Get returns a snapshot if caller owns the job or the job is unowned.
func (r *Registry) Get(id, owner string) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, err := r.authorizedLocked(id, owner)
	if err != nil {
		return Snapshot{}, err
	}
	return r.snapshotLocked(job), nil
}

// List returns unowned jobs and jobs owned by owner, never another owner's
// records. The returned snapshots are independent copies.
func (r *Registry) List(owner string) []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Snapshot, 0, len(r.jobs))
	for _, job := range r.jobs {
		if job.spec.Owner != "" && job.spec.Owner != owner {
			continue
		}
		result = append(result, r.snapshotLocked(job))
	}
	return result
}

// Read returns output appended since the previous read for this job.
func (r *Registry) Read(id, owner string) (ReadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, err := r.authorizedLocked(id, owner)
	if err != nil {
		return ReadResult{}, err
	}
	text := string(job.output[job.readOffset:])
	job.readOffset = len(job.output)
	return ReadResult{Text: text, Bytes: len(text), Snapshot: r.snapshotLocked(job)}, nil
}

// Write sends bytes to an interactive job's stdin. It is safe for concurrent
// writers and rejects non-interactive jobs rather than silently dropping input.
func (r *Registry) Write(id, owner, input string) error {
	r.mu.Lock()
	job, err := r.authorizedLocked(id, owner)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if job.stdin == nil {
		return errors.New("job is not interactive")
	}
	job.stdinMu.Lock()
	defer job.stdinMu.Unlock()
	if _, err := io.WriteString(job.stdin, input); err != nil {
		return fmt.Errorf("write job stdin: %w", err)
	}
	return nil
}

// Wait blocks until a job settles. A context deadline returns the current
// state without an error, matching a bounded polling operation; caller
// cancellation is returned so interrupted turns remain distinguishable.
func (r *Registry) Wait(ctx context.Context, id, owner string) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	job, err := r.authorizedLocked(id, owner)
	if err != nil {
		r.mu.Unlock()
		return Snapshot{}, err
	}
	done := job.done
	snapshot := r.snapshotLocked(job)
	r.mu.Unlock()
	if isTerminal(snapshot.Status) {
		return snapshot, nil
	}
	select {
	case <-done:
		return r.Get(id, owner)
	case <-ctx.Done():
		current, getErr := r.Get(id, owner)
		if getErr != nil {
			return Snapshot{}, getErr
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return current, nil
		}
		return current, ctx.Err()
	}
}

// Kill requests cancellation and returns immediately. The terminal state is
// published only after the process has actually stopped, so callers can use
// Wait for a deterministic killed outcome. Repeated calls are idempotent.
func (r *Registry) Kill(id, owner, reason string) (Snapshot, error) {
	r.mu.Lock()
	job, err := r.authorizedLocked(id, owner)
	if err != nil {
		r.mu.Unlock()
		return Snapshot{}, err
	}
	if isTerminal(job.status) || job.status == StatusStopping {
		snapshot := r.snapshotLocked(job)
		r.mu.Unlock()
		return snapshot, nil
	}
	job.status = StatusStopping
	job.detail = strings.TrimSpace(reason)
	cmd := job.cmd
	r.mu.Unlock()
	if err := killProcessTree(cmd); err != nil {
		// The process may have exited between the state transition and kill. The
		// waiter remains authoritative and will publish completed/failed/killed.
		return r.Get(id, owner)
	}
	return r.Get(id, owner)
}

// Close stops all jobs and releases registry resources. It is safe to call
// more than once.
func (r *Registry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	jobs := make([]*trackedJob, 0, len(r.jobs))
	for _, job := range r.jobs {
		jobs = append(jobs, job)
	}
	r.mu.Unlock()
	for _, job := range jobs {
		_, _ = r.Kill(job.id, job.spec.Owner, "registry closed")
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-deadline.C:
			return errors.New("timed out waiting for background jobs to stop")
		}
	}
	r.mu.Lock()
	r.jobs = make(map[string]*trackedJob)
	r.mu.Unlock()
	return nil
}

func (r *Registry) activeCountLocked(owner string) int {
	count := 0
	for _, job := range r.jobs {
		if job.spec.Owner == owner && (job.status == StatusRunning || job.status == StatusStopping) {
			count++
		}
	}
	return count
}

func (r *Registry) authorizedLocked(id, owner string) (*trackedJob, error) {
	job, ok := r.jobs[id]
	if !ok {
		return nil, fmt.Errorf("unknown job %q", id)
	}
	if job.spec.Owner != "" && job.spec.Owner != owner {
		return nil, fmt.Errorf("job %q belongs to another owner", id)
	}
	return job, nil
}

func (r *Registry) snapshotLocked(job *trackedJob) Snapshot {
	return Snapshot{
		ID:              job.id,
		Kind:            job.spec.Kind,
		Label:           job.spec.Label,
		Owner:           job.spec.Owner,
		Status:          job.status,
		Detail:          job.detail,
		StartedAt:       job.startedAt,
		FinishedAt:      job.finishedAt,
		OutputBytes:     len(job.output),
		OutputTruncated: job.truncated,
		ExitCode:        job.exitCode,
	}
}

func (r *Registry) capture(job *trackedJob, stdout, stderr io.ReadCloser) {
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go r.copyOutput(job, stdout, stdoutDone)
	go r.copyOutput(job, stderr, stderrDone)
	// Let the readers drain the OS pipes before Wait closes them.  Under a
	// loaded POSIX runner, waiting first can discard the final buffered records
	// even though the child exits successfully.
	<-stdoutDone
	<-stderrDone
	err := job.cmd.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if isTerminal(job.status) {
		return
	}
	if job.timer != nil {
		job.timer.Stop()
	}
	job.exitCode = exitCode(err)
	if job.status == StatusStopping {
		job.status = StatusKilled
		if job.detail == "" {
			job.detail = "process stopped"
		}
	} else if err == nil {
		job.status = StatusCompleted
	} else {
		job.status = StatusFailed
		job.detail = fmt.Sprintf("process exited with code %d", job.exitCode)
	}
	job.finishedAt = time.Now().UTC()
	if job.stdin != nil {
		job.stdinMu.Lock()
		_ = job.stdin.Close()
		job.stdinMu.Unlock()
	}
	close(job.done)
}

func (r *Registry) copyOutput(job *trackedJob, reader io.Reader, done chan<- struct{}) {
	defer close(done)
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			r.mu.Lock()
			job.output = append(job.output, buffer[:n]...)
			if len(job.output) > job.spec.OutputLimitBytes {
				job.output = boundedOutput(job.output, job.spec.OutputLimitBytes)
				if job.readOffset > len(job.output) {
					job.readOffset = 0
				}
				job.truncated = true
			}
			r.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func boundedOutput(output []byte, limit int) []byte {
	if limit <= 0 || len(output) <= limit {
		return output
	}
	marker := []byte("\n[output truncated]\n")
	if len(marker) >= limit {
		return append([]byte(nil), output[:limit]...)
	}
	remaining := limit - len(marker)
	head := remaining / 2
	tail := remaining - head
	bounded := make([]byte, 0, limit)
	bounded = append(bounded, output[:head]...)
	bounded = append(bounded, marker...)
	bounded = append(bounded, output[len(output)-tail:]...)
	return bounded
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", powershellUTF8Prefix() + command}
	}
	return "sh", []string{"-c", command}
}

func powershellUTF8Prefix() string {
	return "[Console]::InputEncoding=[Text.UTF8Encoding]::new($false); [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $OutputEncoding=[Console]::OutputEncoding; "
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		pid := strconv.Itoa(cmd.Process.Pid)
		if err := exec.Command("taskkill", "/PID", pid, "/T", "/F").Run(); err == nil {
			return nil
		}
	} else if err := killProcessGroup(cmd.Process.Pid); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func isTerminal(status Status) bool {
	return status == StatusCompleted || status == StatusKilled || status == StatusFailed
}
