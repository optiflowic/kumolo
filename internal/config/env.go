package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// godotenvLoad is a variable so it can be replaced in tests.
var godotenvLoad func(filenames ...string) error = godotenv.Load

// Env holds values resolved from the .env file and environment variables.
type Env struct {
	Port     string
	DataDir  string
	LogLevel string
}

// LoadEnv loads .env if present and returns the resolved environment values.
func LoadEnv() Env {
	if err := godotenvLoad(); err != nil && !os.IsNotExist(err) {
		log.Printf("warn: failed to load .env: %v", err)
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
