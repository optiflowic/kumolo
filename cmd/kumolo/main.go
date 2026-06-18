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

	"github.com/lmittmann/tint"
	"github.com/optiflowic/kumolo/internal/config"
	"github.com/optiflowic/kumolo/internal/server"
	"golang.org/x/term"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const banner = `
  ☁   ☁     ☁      ☁   ☁     ☁   ☁

██╗  ██╗██╗   ██╗███╗   ███╗ ██████╗ ██╗      ██████╗
██║ ██╔╝██║   ██║████╗ ████║██╔═══██╗██║     ██╔═══██╗
█████╔╝ ██║   ██║██╔████╔██║██║   ██║██║     ██║   ██║
██╔═██╗ ██║   ██║██║╚██╔╝██║██║   ██║██║     ██║   ██║
██║  ██╗╚██████╔╝██║ ╚═╝ ██║╚██████╔╝███████╗╚██████╔╝
╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ╚══════╝ ╚═════╝

  ☁      ☁    ☁       ☁     ☁      ☁    ☁

  high-fidelity AWS emulator for local dev   %s
  https://github.com/optiflowic/kumolo

  ☁   ☁     ☁      ☁   ☁     ☁   ☁

`

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Fprintf(os.Stderr, banner, version)

	env := config.LoadEnv()
	buildConfig := config.RegisterFlags(flag.CommandLine, env)
	flag.Parse()
	cfg := buildConfig()

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		slog.Warn("unknown log level, defaulting to info", "level", cfg.LogLevel)
		level = slog.LevelInfo
	}
	_, noColorSet := os.LookupEnv("NO_COLOR")
	noColor := noColorSet || !term.IsTerminal(int(os.Stderr.Fd()))
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:   level,
		NoColor: noColor,
	})))

	dataDir := cfg.DataDir
	if dataDir == "" {
		tmpDir, err := os.MkdirTemp("", "kumolo-*")
		if err != nil {
			return fmt.Errorf("create ephemeral data dir: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				slog.Warn("failed to remove ephemeral data dir", "dir", tmpDir, "err", err)
			}
		}()
		slog.Info("using ephemeral storage", "dir", tmpDir)
		dataDir = tmpDir
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux, cleanup, err := server.NewMux(ctx, dataDir, cfg.LifecycleInterval)
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

	listenErr := make(chan error, 1)
	go func() {
		slog.Info(
			"kumolo listening",
			"version", version,
			"commit", commit,
			"built", date,
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
