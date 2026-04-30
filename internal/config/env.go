package config

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"
)

// Env holds values resolved from the .env file and environment variables.
type Env struct {
	Port              string
	DataDir           string
	LogLevel          string
	LifecycleInterval time.Duration
}

// LoadEnv loads .env if present and returns the resolved environment values.
func LoadEnv() Env {
	return loadEnv(godotenv.Load)
}

func loadEnv(dotenvLoader func(...string) error) Env {
	if err := dotenvLoader(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("failed to load .env", "err", err)
	}

	lifecycleInterval := time.Minute
	if raw := getEnv("KUMOLO_LIFECYCLE_INTERVAL", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		switch {
		case err != nil:
			slog.Warn(
				"invalid KUMOLO_LIFECYCLE_INTERVAL, using default 1m",
				"value",
				raw,
				"err",
				err,
			)
		case d <= 0:
			slog.Warn("KUMOLO_LIFECYCLE_INTERVAL must be positive, using default 1m", "value", raw)
		default:
			lifecycleInterval = d
		}
	}

	return Env{
		Port:              getEnv("KUMOLO_PORT", "5566"),
		DataDir:           getEnv("KUMOLO_DATA_DIR", ""),
		LogLevel:          getEnv("KUMOLO_LOG_LEVEL", "info"),
		LifecycleInterval: lifecycleInterval,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
