package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	codeagentpb "code-agent/gen/codeagentpb"
)

const (
	contextEnvelopeVersion = 1
	maxContextFiles        = 256
	maxContextNodes        = 64
	maxContextEvents       = 64
	maxContextSummaryBytes = 8 << 20
)

var errContextFileLimit = errors.New("context file limit reached")

var contextSkipDirs = map[string]bool{
	".agent": true, ".git": true, ".mypy_cache": true, ".pytest_cache": true,
	".scratch": true, ".venv": true, "__pycache__": true, "eval_results": true,
	"node_modules": true, "runtime": true,
}

func buildContextEnvelope(workingDir string, events []Event) (*codeagentpb.ContextEnvelope, error) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return nil, errors.New("build context envelope: working directory is required")
	}
	rootPath, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("build context envelope: invalid working directory")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("build context envelope: open working directory: %w", err)
	}
	defer root.Close()

	envelope := &codeagentpb.ContextEnvelope{SchemaVersion: contextEnvelopeVersion}
	envelope.P0, err = contextDirectorySummary(root)
	if err != nil {
		return nil, err
	}
	for _, relative := range contextEventPaths(events) {
		if len(envelope.P1) >= maxContextNodes {
			break
		}
		summary, ok := contextNodeSummary(root, relative)
		if !ok {
			continue
		}
		envelope.P1 = append(envelope.P1, summary)
		envelope.P3Candidates = append(envelope.P3Candidates, relative)
	}

	start := len(events) - maxContextEvents
	if start < 0 {
		start = 0
	}
	for _, event := range events[start:] {
		envelope.Events = append(envelope.Events, &codeagentpb.ContextEventSummary{
			Seq: event.Seq, Type: boundedContextText(event.Type, 128),
			Detail: contextEventDetail(event), Checksum: event.Checksum,
		})
	}
	if len(events) > 0 {
		head := events[len(events)-1]
		envelope.LedgerSeq = head.Seq
		envelope.LedgerChecksum = head.Checksum
	}
	return envelope, nil
}

func contextDirectorySummary(root *os.Root) ([]*codeagentpb.ContextFileSummary, error) {
	files := make([]*codeagentpb.ContextFileSummary, 0, maxContextFiles)
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if name == "." {
			return nil
		}
		base := path.Base(name)
		if entry.IsDir() {
			if contextSkipDirs[strings.ToLower(base)] {
				return fs.SkipDir
			}
			return nil
		}
		if len(files) >= maxContextFiles {
			return errContextFileLimit
		}
		if !utf8.ValidString(name) || entry.Type()&os.ModeSymlink != 0 || sensitiveContextPath(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, &codeagentpb.ContextFileSummary{Path: name, SizeBytes: info.Size()})
		return nil
	})
	if err != nil && !errors.Is(err, errContextFileLimit) {
		return nil, fmt.Errorf("build context envelope: scan working directory: %w", err)
	}
	return files, nil
}

func contextNodeSummary(root *os.Root, relative string) (*codeagentpb.ContextFileSummary, bool) {
	if !safeContextPath(relative) || contextPathHasLink(root, relative) {
		return nil, false
	}
	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	summary := &codeagentpb.ContextFileSummary{Path: relative, SizeBytes: info.Size()}
	if info.Size() > maxContextSummaryBytes {
		return summary, true
	}
	content, err := io.ReadAll(io.LimitReader(file, maxContextSummaryBytes+1))
	if err != nil || int64(len(content)) > maxContextSummaryBytes {
		return summary, true
	}
	digest := sha256.Sum256(content)
	summary.Sha256 = hex.EncodeToString(digest[:])
	if len(content) > 0 {
		summary.LineCount = int64(bytes.Count(content, []byte{'\n'}))
		if content[len(content)-1] != '\n' {
			summary.LineCount++
		}
	}
	return summary, true
}

func contextPathHasLink(root *os.Root, relative string) bool {
	current := ""
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func contextEventPaths(events []Event) []string {
	seen := make(map[string]bool)
	paths := make([]string, 0, maxContextNodes)
	add := func(raw string) {
		relative := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
		if len(paths) >= maxContextNodes || seen[relative] || !safeContextPath(relative) {
			return
		}
		seen[relative] = true
		paths = append(paths, relative)
	}
	for _, event := range events {
		switch event.Type {
		case codeModifiedEventType:
			var payload codeModificationPayload
			if json.Unmarshal(event.Payload, &payload) == nil {
				add(payload.Path)
			}
		case "tool/call":
			var payload toolCallPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			var arguments map[string]any
			if json.Unmarshal([]byte(payload.ArgumentsJSON), &arguments) != nil {
				continue
			}
			for _, key := range []string{"path", "file_path"} {
				if value, ok := arguments[key].(string); ok {
					add(value)
				}
			}
		}
	}
	return paths
}

func safeContextPath(value string) bool {
	if value == "" || value == "." || strings.ContainsAny(value, "\x00\r\n:") || !fs.ValidPath(value) {
		return false
	}
	return !sensitiveContextPath(value)
}

func sensitiveContextPath(value string) bool {
	for _, part := range strings.Split(strings.ToLower(strings.ReplaceAll(value, "\\", "/")), "/") {
		if part == ".env" || strings.HasPrefix(part, ".env.") || part == "credentials.json" ||
			part == "id_rsa" || part == "id_ed25519" || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
			return true
		}
	}
	return false
}

func contextEventDetail(event Event) string {
	switch event.Type {
	case codeModifiedEventType:
		var payload codeModificationPayload
		if json.Unmarshal(event.Payload, &payload) == nil && safeContextPath(strings.ReplaceAll(payload.Path, "\\", "/")) {
			return boundedContextText(payload.Operation+" "+payload.Path, 256)
		}
	case "tool/call":
		var payload toolCallPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			return boundedContextText("tool="+payload.ToolName, 256)
		}
	case "tool/result":
		var payload toolResultPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			status := "ok"
			if payload.Error != "" || payload.ExitCode != 0 {
				status = "error"
			}
			return boundedContextText(fmt.Sprintf("tool=%s status=%s exit=%d", payload.ToolName, status, payload.ExitCode), 256)
		}
	case planTodoEventType:
		var payload planTodoPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			return fmt.Sprintf("revision=%d steps=%d todos=%d", payload.Revision, len(payload.Plan.Steps), len(payload.Todos))
		}
	}
	return ""
}

func boundedContextText(value string, maximum int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}
