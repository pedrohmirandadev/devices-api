// Command api runs the Devices API server.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/pedrohmirandadev/devices-api/internal/app"
)

// exit is a variable so tests can observe the exit code.
var exit = os.Exit

func main() {
	exit(run(os.Getenv, os.Stdout, os.Stderr))
}

func run(getenv func(string) string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, getenv, stdout); err != nil {
		fmt.Fprintln(stderr, "error:", err)

		return 1
	}

	return 0
}
