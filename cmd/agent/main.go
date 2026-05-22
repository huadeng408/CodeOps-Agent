package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"code-agent/internal/cli"
	"code-agent/internal/config"
)

func main() {
	ctx := context.Background()

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
