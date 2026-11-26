// Package app is the composition root: it wires configuration, adapters and
// use cases together and runs the HTTP server until the context is canceled.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	appdevice "github.com/pedrohmirandadev/devices-api/internal/application/device"
	"github.com/pedrohmirandadev/devices-api/internal/infrastructure/config"
	"github.com/pedrohmirandadev/devices-api/internal/infrastructure/events"
	"github.com/pedrohmirandadev/devices-api/internal/infrastructure/postgres"
	"github.com/pedrohmirandadev/devices-api/internal/interfaces/httpapi"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
)

// Run starts the service and blocks until ctx is canceled, then shuts down
// gracefully: WebSocket subscribers are disconnected, in-flight requests are
// drained (up to SHUTDOWN_TIMEOUT) and the database pool is closed.
func Run(ctx context.Context, getenv func(string) string, logOutput io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(logOutput, &slog.HandlerOptions{Level: cfg.LogLevel}))

	if cfg.AutoMigrate {
		if err := postgres.Migrate(cfg.DatabaseURL); err != nil {
			return err
		}
		logger.InfoContext(ctx, "database migrations applied")
	}

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	broker := events.NewBroker(cfg.EventBuffer, logger)
	defer broker.Close()

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "devices_state_change_subscribers",
			Help: "Number of connected WebSocket subscribers.",
		}, func() float64 { return float64(broker.Subscribers()) }),
	)

	service := appdevice.NewService(postgres.NewRepository(pool), broker, nil)

	srv := &http.Server{
		Handler: httpapi.NewRouter(httpapi.Dependencies{
			Service:        service,
			Subscriber:     broker,
			DB:             pool,
			Logger:         logger,
			Registry:       registry,
			RequestTimeout: cfg.RequestTimeout,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		logger.Info("shutting down")

		// Hijacked WebSocket connections are not tracked by Shutdown, so close
		// them first through the broker.
		broker.Close()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		shutdownErr <- srv.Shutdown(shutdownCtx)
	}()

	logger.InfoContext(ctx, "http server listening", slog.String("addr", ln.Addr().String()))

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	return <-shutdownErr
}
