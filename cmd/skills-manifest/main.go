package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
