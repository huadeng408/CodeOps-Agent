// Package mineru invokes the MinerU CLI for PDF OCR extraction.
package mineru

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Minute

// Client extracts Markdown from PDF files with MinerU.
type Client struct {
	command string
	backend string
	timeout time.Duration
}

// NewClient creates a MinerU client from the shared CODE_AGENT_MINERU_* settings.
func NewClient() *Client {
	command := strings.TrimSpace(os.Getenv("CODE_AGENT_MINERU_COMMAND"))
	if command == "" {
		command = "mineru"
	}
	backend := strings.TrimSpace(os.Getenv("CODE_AGENT_MINERU_BACKEND"))
	if backend == "" {
		backend = "pipeline"
	}
	return &Client{command: command, backend: backend, timeout: defaultTimeout}
}

// ExtractText runs MinerU in OCR mode and returns its Markdown output.
func (c *Client) ExtractText(ctx context.Context, data []byte, fileName string) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", fmt.Errorf("invalid PDF file: %s", fileName)
	}
	executable, err := exec.LookPath(c.command)
	if err != nil {
		return "", fmt.Errorf("MinerU is required for PDF files (command %q not found); install MinerU or set CODE_AGENT_MINERU_COMMAND: %w", c.command, err)
	}

	workDir, err := os.MkdirTemp("", "code-agent-mineru-")
	if err != nil {
		return "", fmt.Errorf("create MinerU work directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	inputPath := filepath.Join(workDir, "document.pdf")
	if err := os.WriteFile(inputPath, data, 0o600); err != nil {
		return "", fmt.Errorf("write MinerU input: %w", err)
	}
	outputDir := filepath.Join(workDir, "output")

	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, commandArgs(inputPath, outputDir, c.backend)...)
	cmd.Env = withLoopbackBypass(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
		return "", fmt.Errorf("MinerU PDF parsing failed: %w", err)
	}
	markdown, err := collectMarkdown(outputDir)
	if err != nil {
		return "", fmt.Errorf("read MinerU output: %w", err)
	}
	if strings.TrimSpace(markdown) == "" {
		return "", fmt.Errorf("MinerU PDF parsing produced no Markdown")
	}
	return markdown, nil
}

func commandArgs(inputPath, outputDir, backend string) []string {
	return []string{"-p", inputPath, "-o", outputDir, "-m", "ocr", "-b", backend}
}

func collectMarkdown(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if text := strings.TrimSpace(string(content)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func withLoopbackBypass(env []string) []string {
	values := make([]string, 0, 3)
	for _, value := range strings.Split(os.Getenv("NO_PROXY"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	for _, required := range []string{"127.0.0.1", "localhost", "::1"} {
		found := false
		for _, value := range values {
			if strings.EqualFold(value, required) {
				found = true
				break
			}
		}
		if !found {
			values = append(values, required)
		}
	}
	filtered := make([]string, 0, len(env)+1)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !strings.EqualFold(key, "NO_PROXY") {
			filtered = append(filtered, item)
		}
	}
	return append(filtered, "NO_PROXY="+strings.Join(values, ","))
}
