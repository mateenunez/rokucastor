// Command rokucastor casts local video files to a Roku.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/stupside/rokucastor/internal/cli"
)

// Set at build time with -ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := cli.Run(ctx, os.Args, cli.Options{Version: version, Commit: commit}); err != nil {
		fmt.Fprintln(os.Stderr, "rokucastor:", err)
		os.Exit(1)
	}
}
