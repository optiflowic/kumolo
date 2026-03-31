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

// RegisterFlags registers the configuration flags on fs and returns a function
// that builds a Config from the parsed flag values. Call flag.Parse() before
// calling the returned function.
func RegisterFlags(fs *flag.FlagSet) func() *Config {
	port := fs.String("port", getEnv("KUMOLO_PORT", "5566"), "HTTP listen port")
	dataDir := fs.String(
		"data-dir",
		getEnv("KUMOLO_DATA_DIR", "/tmp/kumolo"),
		"Root directory for filesystem storage",
	)
	logLevel := fs.String(
		"log-level",
		getEnv("KUMOLO_LOG_LEVEL", "info"),
		"Log verbosity (debug, info, warn, error)",
	)
	return func() *Config {
		return &Config{
			Port:     *port,
			DataDir:  *dataDir,
			LogLevel: *logLevel,
		}
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
