# go-ffmpeg-hls-swarm Makefile
#
# Usage:
#   make help           Show all available targets
#   make build          Build the Go binary
#   make dev            Enter the Nix development shell
#   make test-origin    Run the test HLS origin server
#
# Prerequisites:
#   - Nix with flakes enabled
#   - Go 1.25+ (provided by nix develop)

# ============================================================================
# Configuration
# ============================================================================

BINARY_NAME := go-ffmpeg-hls-swarm
BINARY_PATH := ./cmd/$(BINARY_NAME)
OUTPUT_DIR  := bin
OUTPUT      := $(OUTPUT_DIR)/$(BINARY_NAME)

# Go settings
GOFLAGS     ?=
LDFLAGS     ?= -s -w

# Streaming settings
STREAM_URL  ?= http://localhost:8080/stream.m3u8

# Nix settings
NIX         := nix
NIX_BUILD   := $(NIX) build
NIX_RUN     := $(NIX) run
NIX_DEVELOP := $(NIX) develop
NIX_CHECK   := $(NIX) flake check

# Colors for pretty output
CYAN  := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RESET := \033[0m

# ============================================================================
# Default target
# ============================================================================

.DEFAULT_GOAL := help

# ============================================================================
# PHONY declarations
# ============================================================================

.PHONY: help
.PHONY: all build build-nix clean
.PHONY: dev shell
.PHONY: run run-with-args
.PHONY: test test-unit test-integration test-integration-interactive
.PHONY: lint fmt fmt-nix check check-nix
.PHONY: test-origin test-origin-low-latency test-origin-4k-abr test-origin-stress
.PHONY: container container-load container-run
.PHONY: swarm-client swarm-client-stress swarm-client-gentle swarm-client-burst swarm-client-extreme
.PHONY: swarm-container swarm-container-load swarm-container-run
.PHONY: git-add

# ============================================================================
# Help
# ============================================================================

help: ## Show this help message
	@echo "$(CYAN)go-ffmpeg-hls-swarm$(RESET) - HLS load testing tool"
	@echo ""
	@echo "$(GREEN)Build & Run:$(RESET)"
	@grep -E '^(build|run|all|clean)[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Development:$(RESET)"
	@grep -E '^(dev|shell|lint|fmt|check)[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Testing:$(RESET)"
	@grep -E '^test[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Test Origin Server:$(RESET)"
	@grep -E '^test-origin[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Swarm Client (Load Tester):$(RESET)"
	@grep -E '^swarm-[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Containers:$(RESET)"
	@grep -E '^container[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(YELLOW)Note:$(RESET) Most commands require Nix with flakes enabled."
	@echo "      Run 'make dev' to enter the development shell first."

# ============================================================================
# Build targets
# ============================================================================

all: build ## Build everything

build: $(OUTPUT_DIR) ## Build Go binary (requires nix develop shell)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUTPUT) $(BINARY_PATH)
	@echo "$(GREEN)Built:$(RESET) $(OUTPUT)"

build-nix: ## Build Go binary using Nix (reproducible)
	$(NIX_BUILD)
	@echo "$(GREEN)Built:$(RESET) ./result/bin/$(BINARY_NAME)"

$(OUTPUT_DIR):
	mkdir -p $(OUTPUT_DIR)

clean: ## Remove build artifacts
	rm -rf $(OUTPUT_DIR) result
	@echo "$(GREEN)Cleaned$(RESET)"

# ============================================================================
# Development shell
# ============================================================================

dev: ## Enter Nix development shell
	$(NIX_DEVELOP)

shell: dev ## Alias for 'dev'

# ============================================================================
# Run targets
# ============================================================================

run: build ## Build and run the binary
	$(OUTPUT)

run-nix: ## Run using Nix
	$(NIX_RUN)

run-with-args: build ## Run with arguments (use: make run-with-args ARGS="...")
	$(OUTPUT) $(ARGS)

# ============================================================================
# Testing
# ============================================================================

test: test-unit ## Run all tests

test-unit: ## Run Go unit tests
	go test -v ./...

test-race: ## Run Go tests with race detector
	go test -v -race ./...

test-coverage: ## Run tests with coverage report
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)Coverage report:$(RESET) coverage.html"

test-integration: ## Run NixOS integration tests (Linux only)
	$(NIX_BUILD) .#checks.x86_64-linux.integration-test
	@echo "$(GREEN)Integration tests passed$(RESET)"

test-integration-interactive: ## Run integration tests interactively (Linux only)
	$(NIX_BUILD) .#checks.x86_64-linux.integration-test.driverInteractive
	./result/bin/nixos-test-driver --interactive

# ============================================================================
# Linting & Formatting
# ============================================================================

lint: ## Run Go linters (requires golangci-lint)
	golangci-lint run ./...

fmt: ## Format Go code
	go fmt ./...
	gofumpt -w .

fmt-nix: ## Format Nix files
	$(NIX) fmt

fmt-all: fmt fmt-nix ## Format all code (Go + Nix)

check: check-go check-nix ## Run all checks

check-go: ## Run Go checks via Nix
	$(NIX_BUILD) .#checks.x86_64-linux.go-lint
	$(NIX_BUILD) .#checks.x86_64-linux.go-test
	$(NIX_BUILD) .#checks.x86_64-linux.go-vet

check-nix: ## Validate Nix flake
	$(NIX_CHECK) --no-build

check-nix-full: ## Run full Nix flake check (builds everything)
	$(NIX_CHECK)

# ============================================================================
# Test Origin Server
# ============================================================================

test-origin: ## Run test HLS origin server (default profile)
	$(NIX_RUN) .#test-origin

test-origin-low-latency: ## Run test origin with low-latency profile
	$(NIX_RUN) .#test-origin-low-latency

test-origin-4k-abr: ## Run test origin with 4K ABR profile
	$(NIX_RUN) .#test-origin-4k-abr

test-origin-stress: ## Run test origin with stress-test profile
	$(NIX_RUN) .#test-origin-stress

# ============================================================================
# Swarm Client targets
# ============================================================================

swarm-client: ## Run swarm client (default: 50 clients)
	$(NIX_RUN) .#swarm-client -- $(STREAM_URL)

swarm-client-stress: ## Run swarm client stress profile (200 clients)
	$(NIX_RUN) .#swarm-client-stress -- $(STREAM_URL)

swarm-client-gentle: ## Run swarm client gentle profile (20 clients)
	$(NIX_RUN) .#swarm-client-gentle -- $(STREAM_URL)

swarm-client-burst: ## Run swarm client burst profile (100 clients, fast ramp)
	$(NIX_RUN) .#swarm-client-burst -- $(STREAM_URL)

swarm-client-extreme: ## Run swarm client extreme profile (500 clients)
	$(NIX_RUN) .#swarm-client-extreme -- $(STREAM_URL)

swarm-container: ## Build swarm client OCI container
	$(NIX_BUILD) .#swarm-client-container
	@echo "$(GREEN)Swarm container built:$(RESET) ./result"
	@echo "Load with: docker load < ./result"

swarm-container-load: swarm-container ## Build and load swarm container into Docker
	docker load < ./result
	@echo "$(GREEN)Swarm container loaded$(RESET)"

swarm-container-run: swarm-container-load ## Build, load, and run swarm container
	@test -n "$(STREAM_URL)" || (echo "$(YELLOW)Usage:$(RESET) make swarm-container-run STREAM_URL=http://origin:8080/stream.m3u8" && exit 1)
	docker run --rm -e STREAM_URL=$(STREAM_URL) -p 9090:9090 go-ffmpeg-hls-swarm:latest

# ============================================================================
# Container targets (Test Origin)
# ============================================================================

container: ## Build test origin OCI container image
	$(NIX_BUILD) .#test-origin-container
	@echo "$(GREEN)Container built:$(RESET) ./result"
	@echo "Load with: docker load < ./result"

container-load: container ## Build and load test origin container into Docker
	docker load < ./result
	@echo "$(GREEN)Container loaded$(RESET)"

container-run: container-load ## Build, load, and run test origin container
	docker run --rm -p 8080:80 test-hls-origin:latest

# ============================================================================
# Git helpers
# ============================================================================

git-add: ## Stage all files for git (useful after nix changes)
	git add -A
	@echo "$(GREEN)All files staged$(RESET)"

# ============================================================================
# Quick recipes
# ============================================================================

quick-test: ## Quick smoke test: build and show version
	@$(MAKE) build
	@echo ""
	@$(OUTPUT) --help 2>/dev/null || $(OUTPUT)

ci: check-nix fmt-nix lint test-unit ## Run CI pipeline locally
	@echo "$(GREEN)CI checks passed$(RESET)"

# ============================================================================
# Full stack recipes
# ============================================================================

full-test: ## Run origin + swarm client (opens two terminals)
	@echo "$(CYAN)Starting full stack test...$(RESET)"
	@echo ""
	@echo "Step 1: Start test origin in background"
	@echo "  make test-origin &"
	@echo ""
	@echo "Step 2: Wait for stream to be ready"
	@echo "  sleep 5"
	@echo ""
	@echo "Step 3: Run swarm client"
	@echo "  make swarm-client STREAM_URL=http://localhost:8080/stream.m3u8"
	@echo ""
	@echo "$(YELLOW)Or use docker-compose:$(RESET)"
	@echo "  See docs/CLIENT_DEPLOYMENT.md for docker-compose.yml example"

info: ## Show project info and available profiles
	@echo "$(CYAN)go-ffmpeg-hls-swarm$(RESET)"
	@echo ""
	@echo "$(GREEN)Test Origin Profiles:$(RESET)"
	@echo "  default        Standard 2s segments, 720p"
	@echo "  low-latency    1s segments, optimized for speed"
	@echo "  4k-abr         Multi-bitrate 4K streaming"
	@echo "  stress-test    Maximum throughput configuration"
	@echo ""
	@echo "$(GREEN)Swarm Client Profiles:$(RESET)"
	@echo "  default        50 clients, 5/sec ramp"
	@echo "  stress         200 clients, 20/sec ramp"
	@echo "  gentle         20 clients, 1/sec ramp"
	@echo "  burst          100 clients, 50/sec ramp (thundering herd)"
	@echo "  extreme        500 clients, 50/sec ramp"
	@echo ""
	@echo "$(GREEN)Documentation:$(RESET)"
	@echo "  docs/TEST_ORIGIN.md       Test HLS origin server"
	@echo "  docs/CLIENT_DEPLOYMENT.md Swarm client containers/VMs"
	@echo "  docs/DESIGN.md            Architecture overview"
