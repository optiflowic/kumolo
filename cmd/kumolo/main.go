package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/optiflowic/kumolo/internal/config"
	"github.com/optiflowic/kumolo/internal/server"
)

func main() {
	env := config.LoadEnv()
	buildConfig := config.RegisterFlags(flag.CommandLine, env)
	flag.Parse()
	cfg := buildConfig()

	mux := server.NewMux()

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
		fmt.Printf(
			"kumolo listening on :%s (data-dir: %s, log-level: %s)\n",
			cfg.Port,
			cfg.DataDir,
			cfg.LogLevel,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("warn: server shutdown: %v", err)
	}
}
