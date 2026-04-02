package server

import (
	"net/http"

	"github.com/optiflowic/kumolo/internal/logging"
	"github.com/optiflowic/kumolo/internal/s3"
)

func NewMux(dataDir string) (http.Handler, func(), error) {
	storage, err := s3.NewStorage(dataDir)
	if err != nil {
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/", s3.NewRouter(storage))
	return logging.Middleware(mux), func() { _ = storage.Close() }, nil
}
