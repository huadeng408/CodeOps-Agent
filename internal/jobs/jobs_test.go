package jobs_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/jobs"
)

func shellScript(body string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", body}
	}
	return "sh", []string{"-c", body}
}

func command(body string) jobs.Spec {
	program, args := shellScript(body)
	return jobs.Spec{Kind: "test", Label: "lifecycle", Program: program, Args: args}
}

func waitForOutput(t *testing.T, registry *jobs.Registry, id, owner, want string) jobs.ReadResult {
	t.Helper()
	// The release gate runs all Go packages concurrently; a busy runner can
	// delay a short-lived shell child even though the registry remains healthy.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		read, err := registry.Read(id, owner)
		if err != nil {
			t.Fatalf("read job output: %v", err)
		}
		if strings.Contains(read.Text, want) {
			return read
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not produce %q", id, want)
	return jobs.ReadResult{}
}

func TestRegistryRunsBackgroundProcessAndReadsOutputIncrementally(t *testing.T) {
	registry := jobs.NewRegistry(t.TempDir(), jobs.Config{MaxConcurrentPerOwner: 4})
	defer registry.Close()

	body := "Write-Output first; Start-Sleep -Milliseconds 80; Write-Output second"
	if runtime.GOOS != "windows" {
		body = "printf 'first\\n'; sleep 0.2; printf 'second\\n'"
	}
	spec := command(body)
	spec.Owner = "session-a"
	spec.Interactive = true
	spec.Label = "stream"
	snapshot, err := registry.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if snapshot.Status != jobs.StatusRunning || snapshot.ID == "" {
		t.Fatalf("unexpected start snapshot: %+v", snapshot)
	}

	first := waitForOutput(t, registry, snapshot.ID, "session-a", "first")
	if strings.Contains(first.Text, "second") {
		t.Fatalf("first incremental read consumed future output: %q", first.Text)
	}
	second := waitForOutput(t, registry, snapshot.ID, "session-a", "second")
	if !strings.Contains(second.Text, "second") {
		t.Fatalf("second incremental read missing output: %q", second.Text)
	}
	if _, err := registry.Read(snapshot.ID, "session-a"); err != nil {
		t.Fatalf("repeat read: %v", err)
	}
	final, err := registry.Wait(context.Background(), snapshot.ID, "session-a")
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if final.Status != jobs.StatusCompleted {
		t.Fatalf("unexpected terminal snapshot: %+v", final)
	}
}

func TestRegistryWaitTimeoutReturnsLiveStateAndKillIsIdempotent(t *testing.T) {
	registry := jobs.NewRegistry(t.TempDir(), jobs.Config{})
	defer registry.Close()

	body := "Start-Sleep -Seconds 10"
	if runtime.GOOS != "windows" {
		body = "sleep 10"
	}
	snapshot, err := registry.Start(context.Background(), command(body))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Keep this a bounded caller wait while allowing the child process to be
	// scheduled on a loaded CI runner before we inspect its live state.
	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	live, err := registry.Wait(waitCtx, snapshot.ID, "")
	if err != nil {
		t.Fatalf("deadline wait must return live state, got %v", err)
	}
	if live.Status != jobs.StatusRunning {
		t.Fatalf("deadline wait status = %s, want running", live.Status)
	}

	killed, err := registry.Kill(snapshot.ID, "", "user requested")
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if killed.Status != jobs.StatusStopping {
		t.Fatalf("kill status = %s, want stopping", killed.Status)
	}
	again, err := registry.Kill(snapshot.ID, "", "duplicate request")
	if err != nil {
		t.Fatalf("idempotent kill: %v", err)
	}
	if again.Status != jobs.StatusStopping && again.Status != jobs.StatusKilled {
		t.Fatalf("second kill status = %s, want stopping or killed", again.Status)
	}
	final, err := registry.Wait(context.Background(), snapshot.ID, "")
	if err != nil {
		t.Fatalf("wait killed process: %v", err)
	}
	if final.Status != jobs.StatusKilled {
		t.Fatalf("final status = %s, want killed", final.Status)
	}
}

func TestRegistryEnforcesOwnerIsolationAndConcurrentLimit(t *testing.T) {
	registry := jobs.NewRegistry(t.TempDir(), jobs.Config{MaxConcurrentPerOwner: 1})
	defer registry.Close()

	body := "Start-Sleep -Seconds 2"
	if runtime.GOOS != "windows" {
		body = "sleep 2"
	}
	owned, err := registry.Start(context.Background(), func() jobs.Spec {
		v := command(body)
		v.Owner = "alice"
		return v
	}())
	if err != nil {
		t.Fatalf("start owned job: %v", err)
	}
	if _, err := registry.Get(owned.ID, "bob"); err == nil {
		t.Fatal("other owner accessed a job")
	}
	for _, visible := range registry.List("bob") {
		if visible.ID == owned.ID {
			t.Fatal("other owner saw an owned job")
		}
	}
	if _, err := registry.Start(context.Background(), func() jobs.Spec {
		v := command(body)
		v.Owner = "alice"
		return v
	}()); err == nil {
		t.Fatal("concurrent owner limit was not enforced")
	}
	if _, err := registry.Kill(owned.ID, "alice", "test cleanup"); err != nil {
		t.Fatalf("cleanup kill: %v", err)
	}
	if _, err := registry.Wait(context.Background(), owned.ID, "alice"); err != nil {
		t.Fatalf("cleanup wait: %v", err)
	}
}

func TestRegistryInteractiveWriteAndBoundedOutput(t *testing.T) {
	registry := jobs.NewRegistry(t.TempDir(), jobs.Config{MaxOutputBytes: 64})
	defer registry.Close()

	body := "$line = [Console]::In.ReadLine(); Write-Output ('got:' + $line); Write-Output ('x' * 200)"
	if runtime.GOOS != "windows" {
		body = "IFS= read -r line; printf 'got:%s\\n' \"$line\"; printf 'x%.0s' $(seq 1 200)"
	}
	spec := command(body)
	spec.Interactive = true
	spec.OutputLimitBytes = 64
	snapshot, err := registry.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start interactive job: %v", err)
	}
	if err := registry.Write(snapshot.ID, "", "ping\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	final, err := registry.Wait(context.Background(), snapshot.ID, "")
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if final.Status != jobs.StatusCompleted || !final.OutputTruncated {
		t.Fatalf("unexpected bounded terminal snapshot: %+v", final)
	}
	read, err := registry.Read(snapshot.ID, "")
	if err != nil {
		t.Fatalf("read bounded output: %v", err)
	}
	if !strings.Contains(read.Text, "got:ping") {
		t.Fatalf("interactive output missing echo: %q", read.Text)
	}
	if read.Bytes > 64 {
		t.Fatalf("retained output bytes = %d, want <= 64", read.Bytes)
	}
}

func TestRegistryRejectsWorkingDirectoryOutsideWorkspace(t *testing.T) {
	registry := jobs.NewRegistry(t.TempDir(), jobs.Config{})
	defer registry.Close()

	spec := command("printf outside")
	spec.WorkingDir = t.TempDir()
	if _, err := registry.Start(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("outside working directory was accepted: %v", err)
	}
}

func ExampleStatus() {
	fmt.Println(jobs.StatusRunning)
	// Output: running
}
