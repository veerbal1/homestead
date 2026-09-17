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
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/veerbal1/homestead/internal/config"
	"github.com/veerbal1/homestead/internal/observability"
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

	shutdown, err := observability.SetupTracing(ctx, cfg.OTLPEndpoint, "homestead")
	if err != nil {
		return fmt.Errorf("tracing setup: %w", err)
	}
	defer shutdown(context.Background())

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("unable to parse database url: %v", err)
	}
	poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("unable to create connection pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		// return fmt.Errorf("unable to reach database: %v", err)
		logger.Warn("unable to reach database", "error", err)
	}
	// slog.Info("connected to postgres")

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("unable to parse redis url: %v", err)
	}
	redisOpts.DialTimeout = 100 * time.Millisecond
	redisOpts.ReadTimeout = 100 * time.Millisecond
	redisOpts.WriteTimeout = 100 * time.Millisecond
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	if err := redisotel.InstrumentTracing(rdb); err != nil {
		return fmt.Errorf("redis tracing: %w", err)
	}

	srv := server.New(store.New(pool), rdb, cfg, logger)
	mux := srv.Routes()

	httpSrv := &http.Server{
		Addr: cfg.Addr,
		Handler: otelhttp.NewHandler(mux, "http.server",
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method + " " + r.URL.Path
			}),
		),
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
