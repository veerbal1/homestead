package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/veerbal1/homestead/internal/config"
	"github.com/veerbal1/homestead/internal/server"
	"github.com/veerbal1/homestead/internal/store"
)

var version = "dev"

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	serverErr := make(chan error, 1)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Version = version

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("unable to create connection pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		// return fmt.Errorf("unable to reach database: %v", err)
		logger.Warn("unable to reach database", "error", err)
	}
	// slog.Info("connected to postgres")

	srv := server.New(store.New(pool), cfg, logger)
	mux := srv.Routes()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	slog.Info("listening", "addr", cfg.Addr)

	select {
	case <-ctx.Done():
		slog.Info("shutting down...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown err: %v", err)
		}
	case err := <-serverErr:
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
