package config

import (
	"flag"
	"os"
)

type Config struct {
	Port     string
	DataDir  string
	LogLevel string
}

func Load() *Config {
	port := flag.String("port", getEnv("KUMOLO_PORT", "4566"), "HTTP listen port")
	dataDir := flag.String("data-dir", getEnv("KUMOLO_DATA_DIR", "/tmp/kumolo"), "Root directory for filesystem storage")
	logLevel := flag.String("log-level", getEnv("KUMOLO_LOG_LEVEL", "info"), "Log verbosity (debug, info, warn, error)")
	flag.Parse()

	return &Config{
		Port:     *port,
		DataDir:  *dataDir,
		LogLevel: *logLevel,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
