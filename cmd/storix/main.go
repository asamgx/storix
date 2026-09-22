// Command storix explains where macOS disk space went.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/fang"

	"github.com/asamgx/storix/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := fang.Execute(ctx, cli.Root(),
		fang.WithVersion(cli.Version),
		fang.WithCommit(cli.Commit()),
		fang.WithNotifySignal(), // run() already handles signals via ctx
	)
	return cli.ExitCode(err)
}
