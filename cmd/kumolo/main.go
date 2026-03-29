package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	addr := ":4566"
	fmt.Printf("kumolo listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
