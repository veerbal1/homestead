package config

import (
	"errors"
	"os"
	"time"
)

type Config struct {
	// Version is stamped at build time via -ldflags (not env).
	Version string

	Addr         string
	DatabaseURL  string
	APIKey       string
	RedisURL     string
	OTLPEndpoint string

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	ReadyzTimeout     time.Duration

	MaxRequestBodyBytes int64
	SlugLength          int
	MaxSlugTries        int
	MaxRequestIDLen     int
}

func Default() Config {
	return Config{
		Addr: ":8080",

		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   10 * time.Second,
		ReadyzTimeout:     2 * time.Second,

		MaxRequestBodyBytes: 1 << 20, // 1MB
		SlugLength:          6,
		MaxSlugTries:        5,
		MaxRequestIDLen:     64,
	}
}

func Load() (Config, error) {
	cfg := Default()

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	cfg.APIKey = os.Getenv("API_KEY")
	cfg.RedisURL = os.Getenv("REDIS_URL")
	cfg.OTLPEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if port := os.Getenv("PORT"); port != "" {
		cfg.Addr = ":" + port
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is not set")
	}
	if cfg.APIKey == "" {
		return Config{}, errors.New("API_KEY is not set")
	}
	return cfg, nil
}
