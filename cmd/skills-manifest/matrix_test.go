package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunWritesProductionExecutionMatrixForBuiltins(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), ".agent", "skills.json")
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{
		"--output", output,
		"--matrix-output", matrixPath,
		"--repo-root", repoRoot,
		"--run-id", "skills-matrix-test",
	}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("run failed with %d: %s", exitCode, stderr.String())
	}
	matrix := readMatrix(t, matrixPath)
	if matrix.Status != "VERIFIED" {
		t.Fatalf("matrix status = %s", matrix.Status)
	}
	if matrix.Execution.Denominator < 40 || matrix.Execution.Passed != matrix.Execution.Denominator {
		t.Fatalf("unexpected matrix denominator: %+v", matrix.Execution)
	}
	if len(matrix.Execution.Failures) != 0 || matrix.Execution.LazyBodyLoads != matrix.Execution.Denominator {
		t.Fatalf("matrix did not load every Skill: %+v", matrix.Execution)
	}
	if !matrix.Execution.ProductionLoader || !matrix.Execution.MetadataOnlyDiscovery {
		t.Fatalf("production metadata checks failed: %+v", matrix.Execution)
	}
	if len(matrix.SourcePin.GitSHA) != 40 || len(matrix.SourcePin.DirtyHash) != 64 {
		t.Fatalf("invalid source pin: %+v", matrix.SourcePin)
	}
	if len(matrix.Skills) != matrix.Execution.Denominator {
		t.Fatalf("matrix entries = %d", len(matrix.Skills))
	}
	for _, item := range matrix.Skills {
		if len(item.BodySHA256) != 64 {
			t.Fatalf("Skill %s has no body pin", item.Name)
		}
	}
	body, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(`"prompt"`)) || bytes.Contains(body, []byte("Split the request into bounded work units")) {
		t.Fatal("matrix leaked a Skill instruction body")
	}
	if stdout.String() != output+"\n"+matrixPath+"\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunRejectsManifestAndMatrixUsingSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{
		"--output", path,
		"--matrix-output", path,
	}, &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "must be different files") {
		t.Fatalf("missing same-file error: %q", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("same-file rejection wrote an output: %v", err)
	}
}

func TestRunRejectsFutureOutputsThroughAliasParents(t *testing.T) {
	temporary := t.TempDir()
	realDirectory := filepath.Join(temporary, "real")
	aliasDirectory := filepath.Join(temporary, "alias")
	if err := os.Mkdir(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", aliasDirectory, realDirectory).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v, %s", err, output)
		}
	} else if err := os.Symlink(realDirectory, aliasDirectory); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	outputPath := filepath.Join(realDirectory, "skills.json")
	matrixPath := filepath.Join(aliasDirectory, "skills.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{
		"--output", outputPath,
		"--matrix-output", matrixPath,
	}, &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "must be different files") {
		t.Fatalf("missing alias-file error: %q", stderr.String())
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("alias-file rejection wrote an output: %v", err)
	}
}

func TestExecutionMatrixPinsLazyFilesystemBodyWithoutLeakingIt(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	catalog := filepath.Join(temporary, "catalog")
	skillDir := filepath.Join(catalog, "release")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "LAZY_MATRIX_BODY_SENTINEL"
	content := "---\nname: release\ndescription: Project release override.\nallowed-tools: Git, Bash\ndisable-model-invocation: true\nuser-invocable: true\n---\n" + prompt + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(temporary, ".agent", "skills.json")
	matrixPath := filepath.Join(temporary, "matrix.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{
		"--output", output,
		"--matrix-output", matrixPath,
		"--repo-root", repoRoot,
		"--run-id", "skills-matrix-filesystem-test",
		"--project-dir", catalog,
	}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("run failed with %d: %s", exitCode, stderr.String())
	}
	matrix := readMatrix(t, matrixPath)
	wantDigest := sha256.Sum256([]byte(prompt))
	found := false
	for _, item := range matrix.Skills {
		if item.Name == "release" {
			found = true
			if item.ModelInvocable || !item.UserInvocable {
				t.Fatalf("release invocation policy was not preserved: %+v", item)
			}
			if item.BodySHA256 != hex.EncodeToString(wantDigest[:]) {
				t.Fatalf("release body digest = %s", item.BodySHA256)
			}
		}
	}
	if !found {
		t.Fatal("release Skill missing from matrix")
	}
	manifest, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	matrixBody, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), prompt) || strings.Contains(string(matrixBody), prompt) {
		t.Fatal("lazy filesystem body leaked into metadata or matrix")
	}
}

func readMatrix(t *testing.T, path string) executionMatrix {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var matrix executionMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatal(err)
	}
	return matrix
}
