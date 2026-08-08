// Package server wraps net/http.Server with the timeout defaults and graceful
// shutdown behaviour every entrypoint in this repo needs, plus the signal-driven
// run loop that ties shutdown to SIGINT/SIGTERM.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Config controls listen address and the timeouts applied to every connection.
type Config struct {
	Port              string        `env:"PORT" envDefault:"8080"`
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"10s"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT" envDefault:"10s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT" envDefault:"30s"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

// Server is a thin wrapper around http.Server adding a RunWithContext helper that
// shuts down cleanly when ctx is cancelled.
type Server struct {
	httpServer      *http.Server
	shutdownTimeout time.Duration
}

// New builds a Server bound to handler with cfg's timeouts applied.
func New(cfg Config, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              fmt.Sprintf(":%s", cfg.Port),
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
		},
		shutdownTimeout: cfg.ShutdownTimeout,
	}
}

// RunWithContext starts serving and blocks until either the server fails or ctx is
// cancelled, in which case it shuts down gracefully (draining in-flight requests, up
// to shutdownTimeout) before returning.
func (s *Server) RunWithContext(ctx context.Context) error {
	slog.Info("http server listening", "addr", s.httpServer.Addr)

	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
			defer cancel()
			if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
				slog.Error("http server forced to shut down", "error", err)
			}
		case <-shutdownDone:
		}
	}()

	err := s.httpServer.ListenAndServe()
	close(shutdownDone)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}
