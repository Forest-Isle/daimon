.PHONY: web build run test clean lint fmt docker help

BINARY    := ironclaw
BUILD_DIR := bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
TAGS      := fts5

## web: Build frontend assets
web:
	@if [ -d web/node_modules ]; then \
		cd web && npm run build; \
	else \
		cd web && npm ci --prefer-offline && npm run build; \
	fi

## build: Build the binary
build: web
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/ironclaw

## run: Build and run
run: build
	./$(BUILD_DIR)/$(BINARY) start

## test: Run tests (with race detector)
test:
	CGO_ENABLED=1 go test -tags "$(TAGS)" ./... -v -race -count=1

## test-short: Run tests without race detector (faster, for dev loops)
test-short:
	CGO_ENABLED=1 go test -tags "$(TAGS)" ./... -v -count=1

## lint: Run linter (requires golangci-lint)
lint:
	golangci-lint run ./...

## fmt: Format code
fmt:
	go fmt ./...
	goimports -w . 2>/dev/null || true

## docker: Build Docker image
docker:
	docker build -t $(BINARY):$(VERSION) --build-arg VERSION=$(VERSION) .

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/  /'
