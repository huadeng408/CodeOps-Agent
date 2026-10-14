package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/mcp"
)

func main() {
	ctx := context.Background()
	if len(os.Args) == 2 && os.Args[1] == "--repository-mcp" {
		if err := mcp.RunRepositoryServer(ctx, "."); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}

	app := cli.NewApp(cfg, os.Stdin, os.Stdout, os.Stderr)
	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "run app:", err)
		os.Exit(1)
	}
}
