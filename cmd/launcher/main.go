package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	root, err := os.Getwd()
	if err != nil { return }
	// The desktop shortcut may be launched from any working directory.
	if _, err := os.Stat(filepath.Join(root, "scripts", "start-interview.ps1")); err != nil {
		root = `D:\vscode\localcode`
	}
	script := filepath.Join(root, "scripts", "launch-codeops.ps1")
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
