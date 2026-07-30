package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"code-agent/internal/rag"
)

func (a *App) handleIngestCommand(ctx context.Context, raw string) {
	argument := unquoteSlashArgument(slashArgs(raw, 1))
	if argument == "" {
		a.renderer.PrintLine("usage: /ingest <path>")
		return
	}
	if !a.cfg.RAGEnabled || a.ragIngester == nil {
		a.renderer.PrintLine("ingest failed: RAG ingestion is unavailable")
		return
	}

	file, name, err := a.openIngestFile(argument)
	if err != nil {
		a.renderer.PrintLine("ingest failed: " + err.Error())
		return
	}
	defer file.Close()
	result, err := a.ragIngester.IngestFile(ctx, file, name)
	if err != nil {
		if errors.Is(err, rag.ErrUnavailable) {
			a.renderer.PrintLine("ingest failed: RAG ingestion is unavailable")
			return
		}
		a.renderer.PrintLine("ingest failed: RAG ingestion request failed")
		return
	}
	if result == nil {
		a.renderer.PrintLine("ingest failed: RAG service returned an empty result")
		return
	}

	lines := []string{
		"file: " + result.FileName,
		"md5: " + result.FileMD5,
	}
	if strings.TrimSpace(result.Message) != "" {
		lines = append(lines, "message: "+result.Message)
	}
	a.renderer.PrintBlock("ingest", lines)
}

func (a *App) openIngestFile(argument string) (*os.File, string, error) {
	root := strings.TrimSpace(a.cfg.ProjectRoot)
	if root == "" && a.executor != nil {
		root = a.executor.Root
	}
	canonicalRoot, err := canonicalWorkspacePath(root)
	if err != nil {
		return nil, "", errors.New("workspace is unavailable")
	}

	base := strings.TrimSpace(a.cfg.WorkingDir)
	if a.executor != nil {
		base = a.executor.WorkingDir()
	}
	if base == "" {
		base = canonicalRoot
	}
	candidate := argument
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return nil, "", errors.New("invalid path")
	}
	file, err := openVerifiedIngestFile(candidate, canonicalRoot)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return nil, "", errors.New("file does not exist")
		case errors.Is(err, errIngestPathOutsideWorkspace):
			return nil, "", errors.New("path is outside the workspace")
		case errors.Is(err, errIngestPathNotRegular):
			return nil, "", errors.New("path is not a regular file")
		default:
			return nil, "", errors.New("cannot open file")
		}
	}
	return file, filepath.Base(candidate), nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func unquoteSlashArgument(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if (first == '\'' || first == '"') && last == first {
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}
