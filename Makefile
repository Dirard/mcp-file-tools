.PHONY: build test lint clean install run help

BINARY_NAME=mcp-file-tools
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"
BUILD_FLAGS?=-trimpath -buildvcs=false
CGO_ENABLED?=0

## build: Build the binary
build:
	CGO_ENABLED=$(CGO_ENABLED) go build $(BUILD_FLAGS) $(LDFLAGS) -o $(BINARY_NAME) ./cmd/mcp-file-tools

## test: Run tests
test:
	go test -v -race ./...

## test-cover: Run tests with coverage
test-cover:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

## lint: Run linters
lint:
	go vet ./...
	go fmt ./...

## clean: Remove build artifacts
clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME).exe
	rm -f coverage.out coverage.html
	rm -rf dist/

## install: Install binary to GOPATH/bin
install:
	CGO_ENABLED=$(CGO_ENABLED) go install $(BUILD_FLAGS) $(LDFLAGS) ./cmd/mcp-file-tools

## run: Build and run
run: build
	./$(BINARY_NAME)

## tidy: Tidy go modules
tidy:
	go mod tidy

## build-all: Build for all platforms
build-all:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(BUILD_FLAGS) $(LDFLAGS) -o dist/$(BINARY_NAME)_windows_amd64.exe ./cmd/mcp-file-tools
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build $(BUILD_FLAGS) $(LDFLAGS) -o dist/$(BINARY_NAME)_windows_arm64.exe ./cmd/mcp-file-tools
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(BUILD_FLAGS) $(LDFLAGS) -o dist/$(BINARY_NAME)_darwin_amd64 ./cmd/mcp-file-tools
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(BUILD_FLAGS) $(LDFLAGS) -o dist/$(BINARY_NAME)_darwin_arm64 ./cmd/mcp-file-tools
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) $(LDFLAGS) -o dist/$(BINARY_NAME)_linux_amd64 ./cmd/mcp-file-tools
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(BUILD_FLAGS) $(LDFLAGS) -o dist/$(BINARY_NAME)_linux_arm64 ./cmd/mcp-file-tools

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/ /'

.DEFAULT_GOAL := help
