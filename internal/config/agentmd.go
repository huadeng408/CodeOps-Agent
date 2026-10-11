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
	directories := []string{}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		directories = append(directories, filepath.Join(home, ".agent"))
	}

	if projectRoot != "" {
		directories = append(directories, projectRoot)
	}

	if workingDir != "" {
		directories = append(directories, workingDir)
	}

	seen := map[string]struct{}{}
	instructions := make([]InstructionSource, 0, len(directories))
	for _, directory := range directories {
		if directory == "" {
			continue
		}
		abs := absPathOr(directory, "")
		if abs == "" {
			abs = directory
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		path := filepath.Join(abs, "AGENTS.md")
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			path = filepath.Join(abs, "AGENT.md")
			content, err = os.ReadFile(path)
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read instructions %s: %w", path, err)
		}
		instructions = append(instructions, InstructionSource{Path: path, Content: string(content)})
	}

	return instructions, nil
}
