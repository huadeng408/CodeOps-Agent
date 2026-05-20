package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type InstructionSource struct {
	Path    string
	Content string
}

func LoadInstructions(projectRoot, workingDir string) ([]InstructionSource, error) {
	paths := []string{}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".agent", "AGENT.md"))
	}

	if projectRoot != "" {
		paths = append(paths, filepath.Join(projectRoot, "AGENT.md"))
	}

	if workingDir != "" {
		paths = append(paths, filepath.Join(workingDir, "AGENT.md"))
	}

	seen := map[string]struct{}{}
	instructions := make([]InstructionSource, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs := absPathOr(path, "")
		if abs == "" {
			abs = path
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		content, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read AGENT.md %s: %w", abs, err)
		}
		instructions = append(instructions, InstructionSource{Path: abs, Content: string(content)})
	}

	return instructions, nil
}
