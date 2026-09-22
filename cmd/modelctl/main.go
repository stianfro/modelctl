package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/stianfro/modelctl/internal/cli"
)

// Set by the release build.
var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := cli.New(os.Stdin, os.Stdout, os.Stderr)
	command.Version = version
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "modelctl:", err)
		return cli.ExitCode(err)
	}
	return 0
}
