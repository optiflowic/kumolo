.PHONY: all install build run fmt vet lint test clean

BUILD_DIR = build
BINARY_NAME = $(BUILD_DIR)/kumolo

all: tidy fmt vet lint test build

install:
	go mod download

tidy:
	go mod tidy

build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BINARY_NAME) ./cmd/kumolo

run:
	go run ./cmd/kumolo

fmt:
	go fmt ./...
	go tool golines --base-formatter=gofmt -w .

vet:
	go vet ./...

lint:
	go tool golangci-lint run ./...
	go tool gosec -quiet ./...

test:
	go test ./...

clean:
	rm -rf $(BUILD_DIR)
