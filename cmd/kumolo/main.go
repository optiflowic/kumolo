package main

import (
	"context"
	"errors"
	"flag"
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

	mux, cleanup, err := server.NewMux(cfg.DataDir)
	if err != nil {
		slog.Error("failed to initialize storage", "err", err)
		os.Exit(1)
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

	go func() {
		slog.Info(
			"kumolo listening",
			"port",
			cfg.Port,
			"data-dir",
			cfg.DataDir,
			"log-level",
			cfg.LogLevel,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown", "err", err)
	}
}
