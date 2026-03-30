package server

import (
	"net/http"

	"github.com/optiflowic/kumolo/internal/s3"
)

func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", s3.NewRouter())
	return mux
}
