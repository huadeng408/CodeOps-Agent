package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SpillRef is an opaque, content-addressed locator for a complete tool result.
// It deliberately contains no filesystem path or user-provided filename.
type SpillRef struct {
	Locator  string `json:"locator"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	ToolName string `json:"tool_name,omitempty"`
}

type SpillRequest struct {
	ToolName string
	Content  string
}

// SpillStore persists redacted complete tool results outside the model surface.
type SpillStore interface {
	Save(context.Context, SpillRequest) (SpillRef, error)
	Load(context.Context, string) (string, error)
}

// FileSpillStore is a local spill backend. Files are private to the current
// user and addressed by a SHA-256 digest, so callers cannot supply a path.
type FileSpillStore struct {
	root string
}

func NewFileSpillStore(root string) (*FileSpillStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("spill root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve spill root: %w", err)
	}
	return &FileSpillStore{root: abs}, nil
}

func (s *FileSpillStore) Save(ctx context.Context, request SpillRequest) (SpillRef, error) {
	if err := ctx.Err(); err != nil {
		return SpillRef{}, err
	}
	if strings.TrimSpace(request.ToolName) == "" {
		return SpillRef{}, errors.New("spill tool name is required")
	}
	digest := sha256.Sum256([]byte(request.Content))
	hash := hex.EncodeToString(digest[:])
	ref := SpillRef{
		Locator:  "spill://" + hash,
		SHA256:   hash,
		Bytes:    int64(len(request.Content)),
		ToolName: request.ToolName,
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return SpillRef{}, fmt.Errorf("create spill root: %w", err)
	}
	path := filepath.Join(s.root, hash+".txt")
	if existing, err := os.ReadFile(path); err == nil {
		if sha256.Sum256(existing) != digest {
			return SpillRef{}, errors.New("existing spill content hash mismatch")
		}
		return ref, nil
	} else if !os.IsNotExist(err) {
		return SpillRef{}, fmt.Errorf("inspect spill: %w", err)
	}

	tmp, err := os.CreateTemp(s.root, ".spill-*.tmp")
	if err != nil {
		return SpillRef{}, fmt.Errorf("create spill temporary file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return SpillRef{}, fmt.Errorf("set spill permissions: %w", err)
	}
	if _, err := io.WriteString(tmp, request.Content); err != nil {
		cleanup()
		return SpillRef{}, fmt.Errorf("write spill: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return SpillRef{}, fmt.Errorf("sync spill: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return SpillRef{}, fmt.Errorf("close spill: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		if existing, readErr := os.ReadFile(path); readErr == nil && sha256.Sum256(existing) == digest {
			return ref, nil
		}
		return SpillRef{}, fmt.Errorf("publish spill: %w", err)
	}
	return ref, nil
}

func (s *FileSpillStore) Load(ctx context.Context, locator string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hash, err := parseSpillLocator(locator)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(s.root, hash+".txt"))
	if err != nil {
		return "", fmt.Errorf("load spill: %w", err)
	}
	if actual := sha256.Sum256(data); hex.EncodeToString(actual[:]) != hash {
		return "", errors.New("spill content hash mismatch")
	}
	return string(data), nil
}

func parseSpillLocator(locator string) (string, error) {
	if !strings.HasPrefix(locator, "spill://") {
		return "", errors.New("invalid spill locator")
	}
	hash := strings.TrimPrefix(locator, "spill://")
	if len(hash) != sha256.Size*2 {
		return "", errors.New("invalid spill locator")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", errors.New("invalid spill locator")
	}
	return hash, nil
}

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(["']?\b(?:api[_-]?key|access[_-]?key|secret|token|password|passwd)\b["']?\s*[:=]\s*["']?)([^\s,;"']+)(["']?)`)
	bearerPattern           = regexp.MustCompile(`(?i)(\bBearer\s+)([^\s,;]+)`)
	urlCredentialPattern    = regexp.MustCompile(`(?i)(://[^/@\s:]+:)([^/@\s]+)(@)`)
)

// RedactSensitive removes common credential forms while preserving the key
// name and surrounding diagnostics. It is intentionally conservative: paths,
// source code and ordinary words such as "secret" are not changed unless they
// are used as an assignment or authorization value.
func RedactSensitive(value string) string {
	value = bearerPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = secretAssignmentPattern.ReplaceAllString(value, `${1}[REDACTED]${3}`)
	value = urlCredentialPattern.ReplaceAllString(value, `${1}[REDACTED]${3}`)
	return value
}

func redactToolText(value string) string {
	return RedactSensitive(value)
}
