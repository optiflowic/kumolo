package config

import "flag"

type Config struct {
	Port     string
	DataDir  string
	LogLevel string
}

// RegisterFlags registers flags on fs and returns a builder.
// Call flag.Parse() before invoking the returned function.
func RegisterFlags(fs *flag.FlagSet, env Env) func() Config {
	port := fs.String("port", env.Port, "HTTP listen port")
	dataDir := fs.String(
		"data-dir",
		env.DataDir,
		"Root directory for filesystem storage",
	)
	logLevel := fs.String(
		"log-level",
		env.LogLevel,
		"Log verbosity (debug, info, warn, error)",
	)
	return func() Config {
		return Config{
			Port:     *port,
			DataDir:  *dataDir,
			LogLevel: *logLevel,
		}
	}
}
