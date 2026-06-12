.PHONY: build build-bin run test test-short test-coverage lint fmt docker clean help

BINARY    := daimon
BUILD_DIR := bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
TAGS      := fts5
COVERAGE  := coverage.out

## build: Build the binary
build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/daimon

## build-bin: Build binary only (alias of build, kept for CI compatibility)
build-bin:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/daimon

## run: Build and run
run: build
	./$(BUILD_DIR)/$(BINARY) start

## test: Run all tests (with race detector)
test:
	CGO_ENABLED=1 go test -tags "$(TAGS)" ./... -v -race -count=1

## test-short: Run tests without race detector (faster, for dev loops)
test-short:
	CGO_ENABLED=1 go test -tags "$(TAGS)" ./... -v -count=1

## test-coverage: Run tests with coverage profile
test-coverage:
	CGO_ENABLED=1 go test -tags "$(TAGS)" ./... -coverprofile=$(COVERAGE) -covermode=atomic -count=1
	@echo "Coverage report: go tool cover -html=$(COVERAGE)"

## lint: Run linter (requires golangci-lint)
lint:
	golangci-lint run ./...

## fmt: Format code
fmt:
	go fmt ./...
	goimports -w . 2>/dev/null || true

## vet: Run go vet
vet:
	go vet ./...

## docker: Build Docker image
docker:
	docker build -t $(BINARY):$(VERSION) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		.

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR) $(COVERAGE)

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/  /'
