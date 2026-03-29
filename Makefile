.PHONY: build run test clean

build:
	go build -o build/kumolo ./cmd/kumolo

run:
	go run ./cmd/kumolo

test:
	go test ./...

clean:
	rm -rf build/
