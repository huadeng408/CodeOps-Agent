// Package corpus implements the official technical corpus loader: staged
// checkout, license gate, pilot sampling and idempotent checkpointing.
package corpus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Stager checks out a pinned upstream commit into a staging directory and
// verifies the license evidence at that exact commit. It never writes to the
// global git config or switches system proxies.
type Stager struct {
	// GitPath overrides the git executable (default "git"); used by tests.
	GitPath string
}

// StageResult reports what the stager verified.
type StageResult struct {
	// HeadCommit is the actual checked-out HEAD (must equal the pinned commit).
	HeadCommit string
	// LicensePath is the repo-relative license file path.
	LicensePath string
	// LicenseSHA256 is the sha256 of the license file at that commit.
	LicenseSHA256 string
}

// Stage clones (or updates) the source repository into stagingDir, checks
// out the pinned commit, and verifies the license file hash. It refuses
// to proceed when the checkout HEAD differs from the pinned commit or when
// the license hash mismatches the manifest.
func (s *Stager) Stage(ctx context.Context, spec SourceSpec, stagingDir string) (*StageResult, error) {
	if err := ValidateSourceGate(spec.SourceGate()); err != nil {
		return nil, err
	}
	git := s.GitPath
	if git == "" {
		git = "git"
	}

	repoDir := filepath.Join(stagingDir, spec.SourceID)
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(stagingDir, 0o755); err != nil {
			return nil, fmt.Errorf("create staging dir: %w", err)
		}
		if err := runGit(ctx, git, "", "clone", "--quiet", spec.RepositoryURL, repoDir); err != nil {
			return nil, fmt.Errorf("clone %s: %w", spec.RepositoryURL, err)
		}
	}
	if err := runGit(ctx, git, repoDir, "fetch", "--quiet", "origin"); err != nil {
		return nil, fmt.Errorf("fetch origin: %w", err)
	}
	if err := runGit(ctx, git, repoDir, "checkout", "--quiet", "--detach", spec.SourceCommit); err != nil {
		return nil, fmt.Errorf("checkout %s: %w", spec.SourceCommit, err)
	}

	head, err := gitOutput(ctx, git, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if head != spec.SourceCommit {
		return nil, &GateError{SourceID: spec.SourceID, Reason: fmt.Sprintf("checked out HEAD %s != pinned %s", head, spec.SourceCommit)}
	}

	licenseData, err := gitBytes(ctx, git, repoDir, "show", fmt.Sprintf("%s:%s", spec.SourceCommit, spec.LicensePath))
	if err != nil {
		return nil, &GateError{SourceID: spec.SourceID, Reason: fmt.Sprintf("license file %s not found at commit: %v", spec.LicensePath, err)}
	}
	licenseHash := HashBytes(licenseData)
	if licenseHash != spec.LicenseSHA256 {
		return nil, &GateError{SourceID: spec.SourceID, Reason: fmt.Sprintf("license hash mismatch: manifest %s, checked out %s", spec.LicenseSHA256, licenseHash)}
	}

	return &StageResult{
		HeadCommit:    head,
		LicensePath:   spec.LicensePath,
		LicenseSHA256: licenseHash,
	}, nil
}

func runGit(ctx context.Context, git, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, git, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOutput(ctx context.Context, git, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, git, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return string(trimSpaceBytes(out)), nil
}

func gitBytes(ctx context.Context, git, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, git, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %v: %w", args, err)
	}
	return out, nil
}

func trimSpaceBytes(b []byte) string {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return string(b[start:end])
}
