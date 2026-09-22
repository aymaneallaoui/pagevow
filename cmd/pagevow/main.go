// Command pagevow runs browser tests written as goals.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aymaneallaoui/pagevow/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	injector := cli.NewContainer(cli.SystemOptions())
	code := cli.HandleError(os.Stderr, cli.NewRootCommand(injector).ExecuteContext(ctx))
	if report := injector.Shutdown(); !report.Succeed && code == cli.ExitOK {
		return cli.HandleError(os.Stderr, report)
	}
	return code
}
