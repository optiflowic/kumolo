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
	golangci-lint fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Run 'make fmt' to fix formatting"; exit 1)
	@golangci-lint fmt --diff ./... || (echo "Run 'make fmt' to fix formatting"; exit 1)

vet:
	go vet ./...

lint:
	golangci-lint run ./...

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
	./e2e/aws-cli/dynamodb-streams-restart.sh
	./e2e/aws-cli/kms.sh
	./e2e/aws-cli/sts.sh
	./e2e/aws-cli/cognito.sh
	./e2e/aws-cli/cors.sh

# e2e-terraform applies three times: initial create, then a toggle of
# admin_user_enabled/admin_given_name to exercise AdminUpdateUserAttributes/
# AdminEnableUser/AdminDisableUser, then a revert to defaults before destroy. The final
# targeted `terraform plan -detailed-exitcode` is a regression guard for #555, covering both
# fixes it shipped: aws_cognito_user_pool.mfa_required (declares
# software_token_mfa_configuration explicitly) guards the original bug — SetUserPoolMfaConfig
# always reporting Enabled: false regardless of input, which showed up as a perpetual
# terraform plan diff; aws_cognito_user_pool.main/region_test (declare no MFA block at all)
# guard a second, related fix — kumolo used to always include SoftwareTokenMfaConfiguration
# in Get/SetUserPoolMfaConfig responses instead of omitting it for a pool that's never called
# SetUserPoolMfaConfig, which showed up as the same kind of perpetual diff on any pool that
# doesn't manage this block. Scoped with -target rather than a full-project plan only to skip
# an unrelated, pre-existing drift on aws_s3_object.readme's tags (not a Cognito/MFA issue).
e2e-terraform:
	./e2e/terraform/cleanup.sh
	cd e2e/terraform && \
	  rm -f terraform.tfstate terraform.tfstate.backup .terraform.tfstate.lock.info && \
	  { [ -d .terraform ] || terraform init -input=false; } && \
	  terraform apply -auto-approve && \
	  terraform apply -auto-approve -var="admin_user_enabled=false" -var="admin_given_name=Updated" && \
	  terraform apply -auto-approve && \
	  ( terraform plan -input=false -detailed-exitcode -no-color \
	      -target=aws_cognito_user_pool.mfa_required \
	      -target=aws_cognito_user_pool_client.mfa_required \
	      -target=aws_cognito_user_pool.main \
	      -target=aws_cognito_user_pool.region_test; code=$$?; \
	    case $$code in \
	      0) exit 0 ;; \
	      2) echo "ERROR: terraform plan reported drift on a Cognito user pool after a clean re-apply (regression guard for #555)"; exit 1 ;; \
	      *) exit 1 ;; \
	    esac ) && \
	  terraform destroy -auto-approve

verify:
	go mod verify

clean:
	rm -rf $(BUILD_DIR)
