package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/joho/godotenv"
	"github.com/optiflowic/kumolo/internal/config"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	mux := http.NewServeMux()

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	fmt.Printf("kumolo listening on :%s (data-dir: %s, log-level: %s)\n", cfg.Port, cfg.DataDir, cfg.LogLevel)
	log.Fatal(srv.ListenAndServe())
}
