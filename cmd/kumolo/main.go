package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/optiflowic/kumolo/internal/config"
	"github.com/optiflowic/kumolo/internal/server"
)

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("warn: failed to load .env: %v", err)
	}

	cfg := config.Load()

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
