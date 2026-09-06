package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDoPathCompletionUsesWorkspaceDir proves tab completion resolves
// candidates against the configured workspace rather than the process CWD.
func TestDoPathCompletionUsesWorkspaceDir(t *testing.T) {
	workspace := t.TempDir()
	// A uniquely-prefixed file so exactly one candidate matches the word.
	if err := os.WriteFile(filepath.Join(workspace, "alpha-unique.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	// A distractor that does not share the prefix.
	if err := os.MkdirAll(filepath.Join(workspace, "other"), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}

	var out bytes.Buffer
	b := &InputBuffer{writer: &out, prompt: "> "}
	b.SetWorkspaceDir(workspace)

	buf := []rune("alpha-uni")
	cursor := len(buf)
	b.doPathCompletion(&buf, &cursor)

	if got, want := string(buf), "alpha-unique.txt"; got != want {
		t.Fatalf("completion = %q, want %q", got, want)
	}
	if cursor != len("alpha-unique.txt") {
		t.Fatalf("cursor = %d, want %d", cursor, len("alpha-unique.txt"))
	}
}

// TestSetWorkspaceDirStoresValue is a small contract test for the setter used
// by the future NewApp wiring.
func TestSetWorkspaceDirStoresValue(t *testing.T) {
	b := &InputBuffer{}
	if b.workspaceDir != "" {
		t.Fatalf("workspaceDir should start empty, got %q", b.workspaceDir)
	}
	b.SetWorkspaceDir("/some/workspace")
	if b.workspaceDir != "/some/workspace" {
		t.Fatalf("workspaceDir = %q, want %q", b.workspaceDir, "/some/workspace")
	}
}

// TestReadTimeoutReturnsNilOnTimeout verifies the deadline path bounds the
// read: with no bytes arriving, readTimeout returns (nil, nil) within the
// deadline instead of blocking forever. Uses an os.Pipe, whose read end
// supports SetReadDeadline; on platforms where it does not, the timer-based
// fallback still yields the same result.
func TestReadTimeoutReturnsNilOnTimeout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	start := time.Now()
	got, err := readTimeout(r, 20*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("readTimeout err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil bytes on timeout, got %v", got)
	}
	// Must return promptly (well under a second), not block.
	if elapsed > time.Second {
		t.Fatalf("readTimeout blocked for %v, expected to time out fast", elapsed)
	}
}

// TestReadTimeoutReturnsBytesWhenAvailable verifies that bytes already pending
// on f are returned immediately, before the deadline elapses.
func TestReadTimeoutReturnsBytesWhenAvailable(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := w.Write([]byte{0x1b, '['}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := readTimeout(r, time.Second)
	if err != nil {
		t.Fatalf("readTimeout err: %v", err)
	}
	if want := []byte{0x1b, '['}; !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReadTermKeyDecodesUTF8Rune(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	if _, err := w.Write([]byte("你")); err != nil {
		t.Fatalf("write UTF-8 input: %v", err)
	}
	b := &InputBuffer{tty: r}
	got, seq, escaped, err := b.readTermKey()
	if err != nil {
		t.Fatalf("readTermKey: %v", err)
	}
	if got != '你' {
		t.Fatalf("rune = %q (U+%04X), want %q", got, got, '你')
	}
	if seq != "" || escaped {
		t.Fatalf("UTF-8 rune decoded as escape sequence: seq=%q escaped=%v", seq, escaped)
	}
}
