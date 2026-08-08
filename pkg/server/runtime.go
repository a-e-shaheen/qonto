package server

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// RunUntilSignal runs fn (expected to block, e.g. Server.RunWithContext) until a
// SIGINT/SIGTERM is received, then cancels ctx so fn can shut down cooperatively, and
// waits for fn to return before calling cleanup. It returns fn's error, if any.
func RunUntilSignal(ctx context.Context, fn func(ctx context.Context) error, cleanup func(ctx context.Context)) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := fn(ctx)

	if ctx.Err() != nil {
		slog.Info("received shutdown signal, cleaning up")
	}
	cleanup(context.Background())

	return err
}
