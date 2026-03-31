package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
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

	fmt.Printf(
		"kumolo listening on :%s (data-dir: %s, log-level: %s)\n",
		cfg.Port,
		cfg.DataDir,
		cfg.LogLevel,
	)
	log.Fatal(srv.ListenAndServe())
}
