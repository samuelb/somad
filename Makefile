# SomaTUI Makefile
# A terminal UI client for SomaFM internet radio

# Variables
BINARY_NAME=somatui
CMD_PATH=./cmd/somatui
BUILD_DIR=.
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE?=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
# GOPATH the environment variable is usually unset with modern Go; fall back
# to what the toolchain reports so `make install` has a real target directory.
GOBIN?=$(shell go env GOPATH)/bin

# Build flags
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Default target
.PHONY: all
all: build

# Build the application
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Build for current platform with race detection
.PHONY: build-race
build-race:
	@echo "Building with race detector..."
	$(GOBUILD) $(LDFLAGS) -race -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

# Run the application
.PHONY: run
run: build
	@echo "Running $(BINARY_NAME)..."
	$(BUILD_DIR)/$(BINARY_NAME)

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	$(GOTEST) -race ./...

# Run tests with coverage
.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -func=coverage.out

# Run tests with coverage and generate HTML report
.PHONY: test-coverage-html
test-coverage-html:
	@echo "Running tests with coverage report..."
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run benchmarks
.PHONY: benchmark
benchmark:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...

# Run linter
.PHONY: lint
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install from https://golangci-lint.run/usage/install/"; \
		exit 1; \
	fi

# Fix linting issues automatically
.PHONY: lint-fix
lint-fix:
	@echo "Running linter with auto-fix..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --fix ./...; \
	else \
		echo "golangci-lint not installed. Install from https://golangci-lint.run/usage/install/"; \
		exit 1; \
	fi

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning..."
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Download and verify dependencies
.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) verify

# Update dependencies
.PHONY: deps-update
deps-update:
	@echo "Updating dependencies..."
	$(GOMOD) tidy
	$(GOMOD) download

# Install the binary to $GOBIN
.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME) to $(GOBIN)..."
	install -d $(GOBIN)
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOBIN)/
	@echo "Installation complete"

# Uninstall the binary from $GOBIN
.PHONY: uninstall
uninstall:
	@echo "Uninstalling $(BINARY_NAME) from $(GOBIN)..."
	rm -f $(GOBIN)/$(BINARY_NAME)
	@echo "Uninstall complete"

# Format Go code
.PHONY: fmt
fmt:
	@echo "Formatting Go code..."
	gofmt -w -s .
	goimports -w . 2>/dev/null || echo "goimports not installed, skipping"

# Vet Go code
.PHONY: vet
vet:
	@echo "Running go vet..."
	$(GOCMD) vet ./...

# Check for security issues
.PHONY: security
security:
	@echo "Running security scan..."
	@if command -v gosec >/dev/null 2>&1; then \
		gosec ./...; \
	else \
		echo "gosec not installed. Install with: go install github.com/securego/gosec/v2/cmd/gosec@latest"; \
		exit 1; \
	fi

# Run all checks (lint, test, vet)
.PHONY: check
check: lint test vet
	@echo "All checks passed!"

# CI target - runs everything needed for CI
.PHONY: ci
ci: deps test lint build
	@echo "CI checks complete!"

# Development mode - build and run with file watching (requires entr)
.PHONY: dev
dev:
	@echo "Starting development mode (requires 'entr' tool)..."
	@if command -v entr >/dev/null 2>&1; then \
		find . -name "*.go" | entr -r make run; \
	else \
		echo "entr not installed. Install with your package manager (e.g., apt-get install entr)"; \
		exit 1; \
	fi

# Show help
.PHONY: help
help:
	@echo "SomaTUI Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all               Build the application (default)"
	@echo "  build             Build the application"
	@echo "  build-race        Build with race detector enabled"
	@echo "  run               Build and run the application"
	@echo "  test              Run all tests"
	@echo "  test-coverage     Run tests with coverage report"
	@echo "  test-coverage-html Run tests and generate HTML coverage report"
	@echo "  benchmark         Run benchmarks"
	@echo "  lint              Run linter (golangci-lint)"
	@echo "  lint-fix          Run linter with auto-fix"
	@echo "  clean             Remove build artifacts"
	@echo "  deps              Download and verify dependencies"
	@echo "  deps-update       Update dependencies"
	@echo "  install           Install binary to \$$GOBIN"
	@echo "  uninstall         Remove binary from \$$GOBIN"
	@echo "  fmt               Format Go code"
	@echo "  vet               Run go vet"
	@echo "  security          Run security scan (gosec)"
	@echo "  check             Run lint, test, and vet"
	@echo "  ci                Run CI pipeline (deps, test, lint, build)"
	@echo "  dev               Development mode with file watching"
	@echo "  help              Show this help message"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION           Set version (default: git tag or 'dev')"
	@echo "  COMMIT            Set commit hash (default: git sha or 'none')"
	@echo "  DATE              Set build date (default: current UTC time)"
