.PHONY: all install build run fmt fmt-check vet lint test cover integration verify tidy clean

BUILD_DIR = build
BINARY_NAME = $(BUILD_DIR)/kumolo

all: fmt-check vet lint test build

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

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Run 'make fmt' to fix formatting"; exit 1)
	@test -z "$$(go tool golines --base-formatter=gofmt -l .)" || (echo "Run 'make fmt' to fix formatting"; exit 1)

vet:
	go vet ./...

lint:
	go tool golangci-lint run ./...

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

integration:
	go test -race -count=1 ./tests/integration/...

verify:
	go mod verify

clean:
	rm -rf $(BUILD_DIR)
