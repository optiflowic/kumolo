package config

import (
	"errors"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
)

// Env holds values resolved from the .env file and environment variables.
type Env struct {
	Port     string
	DataDir  string
	LogLevel string
}

// LoadEnv loads .env if present and returns the resolved environment values.
func LoadEnv() Env {
	return loadEnv(godotenv.Load)
}

func loadEnv(dotenvLoader func(...string) error) Env {
	if err := dotenvLoader(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("failed to load .env", "err", err)
	}
	return Env{
		Port:     getEnv("KUMOLO_PORT", "5566"),
		DataDir:  getEnv("KUMOLO_DATA_DIR", "/tmp/kumolo"),
		LogLevel: getEnv("KUMOLO_LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
