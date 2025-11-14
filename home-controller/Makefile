.PHONY: help build test test-verbose test-coverage run clean fmt vet lint install-tools

# Variables
BINARY_NAME=home-controller
BUILD_DIR=bin
CONFIG_FILE=config.yaml
GO=go
GOTEST=$(GO) test
GOVET=$(GO) vet
GOFMT=gofmt

# Default target
help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the application
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) .
	@echo "✅ Build successful: $(BUILD_DIR)/$(BINARY_NAME)"

run: ## Run the application (requires config.yaml)
	@if [ ! -f $(CONFIG_FILE) ]; then \
		echo "❌ Error: $(CONFIG_FILE) not found"; \
		exit 1; \
	fi
	@echo "Running $(BINARY_NAME)..."
	$(GO) run . -c $(CONFIG_FILE)

run-build: build ## Build and run the application
	@if [ ! -f $(CONFIG_FILE) ]; then \
		echo "❌ Error: $(CONFIG_FILE) not found"; \
		exit 1; \
	fi
	@echo "Running $(BUILD_DIR)/$(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME) -c $(CONFIG_FILE)

test: ## Run all tests
	@echo "Running tests..."
	$(GOTEST) ./... -v

test-short: ## Run tests without verbose output
	@echo "Running tests (short)..."
	$(GOTEST) ./...

test-verbose: ## Run tests with verbose output
	@echo "Running tests (verbose)..."
	$(GOTEST) ./... -v -race

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	$(GOTEST) ./... -v -race -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -func=coverage.out
	@echo ""
	@echo "To view HTML coverage report, run: make coverage-html"

coverage-html: coverage.out ## Generate HTML coverage report
	@echo "Generating HTML coverage report..."
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report: coverage.html"

fmt: ## Format Go code
	@echo "Formatting code..."
	$(GOFMT) -s -w .
	@echo "✅ Code formatted"

vet: ## Run go vet
	@echo "Running go vet..."
	$(GOVET) ./...
	@echo "✅ Vet passed"

check: fmt vet test-short ## Run fmt, vet, and tests (pre-commit check)
	@echo "✅ All checks passed"

clean: ## Clean build artifacts and coverage files
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html
	@echo "✅ Cleaned"

install-tools: ## Install development tools
	@echo "Installing development tools..."
	$(GO) install golang.org/x/tools/cmd/goimports@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "✅ Tools installed"

lint: ## Run golangci-lint (requires install-tools)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Run 'make install-tools' first"; \
		exit 1; \
	}
	@echo "Running golangci-lint..."
	golangci-lint run ./...
	@echo "✅ Lint passed"

# Development targets
dev: fmt vet test-short ## Quick development cycle (fmt, vet, test)
	@echo "✅ Development checks passed"

ci: check test-coverage ## Run all checks for CI (fmt, vet, full tests with coverage)
	@echo "✅ CI checks passed"

# Docker targets (if needed in the future)
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t $(BINARY_NAME):latest .
	@echo "✅ Docker image built"

.DEFAULT_GOAL := help
