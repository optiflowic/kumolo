.PHONY: all install build run fmt fmt-check vet lint test cover uncovered integration e2e e2e-terraform verify tidy clean

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

uncovered: cover
	@go run tools/uncovered/main.go -exclude cmd/kumolo/main.go

integration:
	go test -race -count=1 -timeout 120s ./tests/integration/...

e2e:
	./e2e/aws-cli/s3.sh
	./e2e/aws-cli/dynamodb.sh
	./e2e/aws-cli/kms.sh
	./e2e/aws-cli/sts.sh
	./e2e/aws-cli/cognito.sh

e2e-terraform:
	./e2e/terraform/cleanup.sh
	cd e2e/terraform && \
	  rm -f terraform.tfstate terraform.tfstate.backup .terraform.tfstate.lock.info && \
	  { [ -d .terraform ] || terraform init -input=false; } && \
	  terraform apply -auto-approve && \
	  terraform destroy -auto-approve

verify:
	go mod verify

clean:
	rm -rf $(BUILD_DIR)
