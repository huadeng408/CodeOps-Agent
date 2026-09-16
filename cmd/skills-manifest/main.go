package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"code-agent/internal/skills"
)

type directoryFlags []string

func (values *directoryFlags) String() string {
	return strings.Join(*values, ",")
}

func (values *directoryFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skills-manifest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write metadata-only Skill manifest here")
	matrixOutput := flags.String("matrix-output", "", "optionally write the production Skill load matrix here")
	repoRoot := flags.String("repo-root", ".", "repository root used for the matrix source pin")
	runID := flags.String("run-id", "skills-matrix", "matrix run identifier")
	globalDir := flags.String("global-dir", "", "optional global Skill directory")
	projectDir := flags.String("project-dir", "", "optional project Skill directory")
	var directories directoryFlags
	flags.Var(&directories, "dir", "optional configured Skill directory (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*output) == "" {
		fmt.Fprintln(stderr, "--output is required")
		return 2
	}
	if strings.TrimSpace(*matrixOutput) != "" {
		same, err := sameOutputFile(*output, *matrixOutput)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if same {
			fmt.Fprintln(stderr, "--output and --matrix-output must be different files")
			return 2
		}
	}

	manager := skills.NewManager()
	if err := manager.Discover(skills.DiscoveryOptions{
		GlobalDir:   *globalDir,
		Directories: directories,
		ProjectDir:  *projectDir,
	}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := manager.WriteManifest(*output); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, *output)
	if strings.TrimSpace(*matrixOutput) != "" {
		matrix, err := writeExecutionMatrix(manager, *output, *matrixOutput, *repoRoot, *runID)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, *matrixOutput)
		if matrix.Status != "VERIFIED" {
			return 1
		}
	}
	return 0
}

func sameOutputFile(left, right string) (bool, error) {
	leftPath, err := filepath.Abs(left)
	if err != nil {
		return false, fmt.Errorf("resolve --output: %w", err)
	}
	rightPath, err := filepath.Abs(right)
	if err != nil {
		return false, fmt.Errorf("resolve --matrix-output: %w", err)
	}
	leftPath = filepath.Clean(leftPath)
	rightPath = filepath.Clean(rightPath)
	if leftPath == rightPath || (runtime.GOOS == "windows" && strings.EqualFold(leftPath, rightPath)) {
		return true, nil
	}
	leftInfo, err := outputFileInfo(leftPath, "--output")
	if err != nil {
		return false, err
	}
	rightInfo, err := outputFileInfo(rightPath, "--matrix-output")
	if err != nil {
		return false, err
	}
	if leftInfo != nil || rightInfo != nil {
		return leftInfo != nil && rightInfo != nil && os.SameFile(leftInfo, rightInfo), nil
	}
	leftParent, leftSuffix, err := futureOutputIdentity(leftPath, "--output")
	if err != nil {
		return false, err
	}
	rightParent, rightSuffix, err := futureOutputIdentity(rightPath, "--matrix-output")
	if err != nil {
		return false, err
	}
	return os.SameFile(leftParent, rightParent) && samePathParts(leftSuffix, rightSuffix), nil
}

func outputFileInfo(path, label string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	entry, linkErr := os.Lstat(path)
	if linkErr == nil && entry.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("inspect %s: dangling symbolic link", label)
	}
	if linkErr != nil && !errors.Is(linkErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect %s: %w", label, linkErr)
	}
	return nil, nil
}

func futureOutputIdentity(path, label string) (os.FileInfo, []string, error) {
	current := filepath.Dir(path)
	reversed := []string{filepath.Base(path)}
	for {
		info, err := os.Stat(current)
		if err == nil {
			for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
				reversed[left], reversed[right] = reversed[right], reversed[left]
			}
			return info, reversed, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("inspect %s parent: %w", label, err)
		}
		entry, linkErr := os.Lstat(current)
		if linkErr == nil && entry.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("inspect %s parent: dangling symbolic link", label)
		}
		if linkErr != nil && !errors.Is(linkErr, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("inspect %s parent: %w", label, linkErr)
		}
		next := filepath.Dir(current)
		if next == current {
			return nil, nil, fmt.Errorf("inspect %s parent: no existing ancestor", label)
		}
		reversed = append(reversed, filepath.Base(current))
		current = next
	}
}

func samePathParts(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] && !(runtime.GOOS == "windows" && strings.EqualFold(left[index], right[index])) {
			return false
		}
	}
	return true
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
