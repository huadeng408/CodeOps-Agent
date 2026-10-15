package main_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLauncherKeepsMutablePathsOutsideInstalledProduct(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "CodeOps-Agent.exe")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(contents)
	files := map[string]string{"CodeOps-Agent.exe": hex.EncodeToString(checksum[:])}
	for _, name := range []string{"bin/code-agent-server.exe", "python/python.exe", "python/python313._pth", "python/python313.zip", "git/cmd/git.exe", "app/orchestrator/server.py", "configs/local-core.yaml", "frontend/dist/index.html"} {
		file := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		files[name] = "f16d05ec6b29248d2c61adb1e9263f78e4f7bace1b955014a2d17872cfe4064d"
	}
	manifest, err := json.Marshal(map[string]any{"schema_version": 1, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bundle-manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CODE_AGENT_PORTABLE_DATA_HOME", "CODE_AGENT_TASK_WORKSPACE_ROOT"} {
		t.Run(key, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary)
			command.Env = []string{"PATH=", "SystemRoot=" + os.Getenv("SystemRoot"), "CODE_AGENT_PORTABLE_DATA_HOME=" + t.TempDir()}
			if key == "CODE_AGENT_PORTABLE_DATA_HOME" {
				command.Env = command.Env[:2]
			}
			command.Env = append(command.Env, key+"="+filepath.Join(directory, "mutable"))
			output, err := command.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "must be outside the verified product directory") {
				t.Fatalf("mutable installation path must be refused before child startup: err=%v deadline=%v output=%q", err, ctx.Err(), output)
			}
			if _, err := os.Stat(filepath.Join(directory, "mutable")); !os.IsNotExist(err) {
				t.Fatalf("refused installation path was mutated: %v", err)
			}
		})
	}
}

func TestLauncherRejectsUnlistedRuntimeFile(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "CodeOps-Agent.exe")
	output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(contents)
	manifest, err := json.Marshal(map[string]any{"schema_version": 1, "files": map[string]string{"CodeOps-Agent.exe": hex.EncodeToString(checksum[:])}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bundle-manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "python313._pth"), []byte("unexpected runtime path"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = []string{"PATH=", "SystemRoot=" + os.Getenv("SystemRoot")}
	output, err = command.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "unlisted or unsafe") {
		t.Fatalf("unlisted runtime injection must be refused: err=%v output=%q", err, output)
	}
}

func TestLauncherRejectsChangedBundledComponent(t *testing.T) {
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "launcher.exe")
	if output, err := exec.Command(goCommand, "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	if err := os.Mkdir(filepath.Join(directory, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bin", "code-agent-server.exe"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	// SHA-256 of the originally packaged text "fixture".
	manifest, err := json.Marshal(map[string]any{"schema_version": 1, "files": map[string]string{
		"bin/code-agent-server.exe": "f16d05ec6b29248d2c61adb1e9263f78e4f7bace1b955014a2d17872cfe4064d",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bundle-manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Dir = t.TempDir()
	command.Env = []string{"PATH=", "SystemRoot=" + os.Getenv("SystemRoot")}
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("modified bundle must be refused before execution: err=%v output=%q", err, output)
	}
}

func TestLauncherRejectsMissingBundleWithoutDeveloperFallback(t *testing.T) {
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	name := "CodeOps-Agent"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, name)
	build := exec.Command(goCommand, "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v: %s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Dir = t.TempDir()
	command.Env = []string{"PATH=", "HOME=" + directory, "USERPROFILE=" + directory,
		"SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir()}
	output, err := command.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "bundle manifest") {
		t.Fatalf("missing relocated bundle must fail without a developer fallback: err=%v output=%q", err, output)
	}
}
