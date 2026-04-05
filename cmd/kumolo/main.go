package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/optiflowic/kumolo/internal/config"
	"github.com/optiflowic/kumolo/internal/logging"
	"github.com/optiflowic/kumolo/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	env := config.LoadEnv()
	buildConfig := config.RegisterFlags(flag.CommandLine, env)
	flag.Parse()
	cfg := buildConfig()

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		slog.Warn("unknown log level, defaulting to info", "level", cfg.LogLevel)
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(logging.NewBracketHandler(os.Stderr, level)))

	dataDir := cfg.DataDir
	if dataDir == "" {
		tmpDir, err := os.MkdirTemp("", "kumolo-*")
		if err != nil {
			return fmt.Errorf("create ephemeral data dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		dataDir = tmpDir
	}

	mux, cleanup, err := server.NewMux(dataDir)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer cleanup()

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenErr := make(chan error, 1)
	go func() {
		slog.Info(
			"kumolo listening",
			"port",
			cfg.Port,
			"data-dir",
			dataDir,
			"log-level",
			cfg.LogLevel,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	select {
	case err := <-listenErr:
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown", "err", err)
	}
	return nil
}
