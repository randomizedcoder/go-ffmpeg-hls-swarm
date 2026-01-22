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
ORIGIN_PORT ?= 8080
STREAM_URL  ?= http://localhost:$(ORIGIN_PORT)/stream.m3u8

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
.PHONY: test-origin test-origin-low-latency test-origin-4k-abr test-origin-stress test-origin-logged test-origin-debug
.PHONY: container container-load container-run
.PHONY: swarm-client swarm-client-stress swarm-client-gentle swarm-client-burst swarm-client-extreme
.PHONY: swarm-container swarm-container-load swarm-container-run
.PHONY: microvm-check-kvm microvm-check-ports microvm-start microvm-start-tap microvm-stop microvm-origin microvm-origin-build microvm-origin-stop microvm-origin-logged microvm-origin-debug microvm-origin-tap microvm-origin-tap-logged
.PHONY: load-test-100-microvm load-test-300-microvm load-test-500-microvm
.PHONY: load-test-50 load-test-100 load-test-300 load-test-500 load-test-1000
.PHONY: network-setup network-teardown network-check
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
	@echo "$(GREEN)MicroVMs (requires KVM):$(RESET)"
	@grep -E '^microvm-(start|stop|check):.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@grep -E '^microvm-origin[^:]*:.*##' $(MAKEFILE_LIST) | head -3 | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Load Tests (local origin):$(RESET)"
	@grep -E '^load-test-[0-9]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Load Tests (MicroVM origin):$(RESET)"
	@grep -E '^load-test-.*-microvm:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Network Setup (high-performance):$(RESET)"
	@grep -E '^network-[^:]*:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*##"}; {printf "  $(CYAN)%-28s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(YELLOW)Note:$(RESET) Most commands require Nix with flakes enabled."
	@echo "      Run 'make dev' to enter the development shell first."
	@echo "      MicroVM targets require KVM (run 'make microvm-check-kvm' to verify)."

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

test-origin: ## Run test HLS origin server (default profile, PORT=$(ORIGIN_PORT))
	PORT=$(ORIGIN_PORT) $(NIX_RUN) .#test-origin

test-origin-low-latency: ## Run test origin with low-latency profile
	PORT=$(ORIGIN_PORT) $(NIX_RUN) .#test-origin-low-latency

test-origin-4k-abr: ## Run test origin with 4K ABR profile
	PORT=$(ORIGIN_PORT) $(NIX_RUN) .#test-origin-4k-abr

test-origin-stress: ## Run test origin with stress-test profile
	PORT=$(ORIGIN_PORT) $(NIX_RUN) .#test-origin-stress

test-origin-logged: ## Run test origin with logging (512k buffer, segments only)
	PORT=$(ORIGIN_PORT) $(NIX_RUN) .#test-origin-logged

test-origin-debug: ## Run test origin with full logging (all requests, gzip)
	PORT=$(ORIGIN_PORT) $(NIX_RUN) .#test-origin-debug

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
# MicroVM targets (requires KVM)
# ============================================================================

microvm-check-kvm: ## Check if KVM is available for MicroVMs
	@echo "$(CYAN)Checking KVM availability...$(RESET)"
	@if [ -e /dev/kvm ]; then \
		echo "$(GREEN)✓ /dev/kvm exists$(RESET)"; \
		ls -la /dev/kvm; \
	else \
		echo "$(YELLOW)✗ /dev/kvm not found$(RESET)"; \
		echo "  Enable KVM: sudo modprobe kvm_intel (or kvm_amd)"; \
		exit 1; \
	fi
	@if grep -qE 'vmx|svm' /proc/cpuinfo 2>/dev/null; then \
		echo "$(GREEN)✓ CPU virtualization supported$(RESET)"; \
	else \
		echo "$(YELLOW)⚠ CPU virtualization flags not found$(RESET)"; \
	fi
	@echo ""
	@echo "$(GREEN)KVM is ready for MicroVMs$(RESET)"

microvm-origin-build: ## Build the test origin MicroVM
	@echo "$(CYAN)Building test origin MicroVM...$(RESET)"
	@echo "This may take a while on first build (downloads NixOS components)"
	$(NIX_BUILD) .#test-origin-vm
	@echo "$(GREEN)MicroVM built successfully$(RESET)"

microvm-start: microvm-check-kvm ## Start MicroVM with health polling (recommended)
	@./scripts/microvm/start.sh

microvm-stop: ## Stop the MicroVM
	@./scripts/microvm/stop.sh

microvm-reset: ## Stop VM and teardown networking (reset to clean state)
	@./scripts/microvm/reset.sh

microvm-reset-full: ## Full reset: stop VM, teardown networking, remove build artifacts
	@./scripts/microvm/reset.sh --full

microvm-check-ports: ## Check if MicroVM ports (17080, 17100, 17113, 17122, 17022) are available
	@echo "$(CYAN)Checking MicroVM port availability (see docs/PORTS.md)...$(RESET)"
	@for port in 17080 17100 17113 17122 17022; do \
		if bash -c "(echo >/dev/tcp/localhost/$$port) 2>/dev/null"; then \
			echo "$(RED)ERROR: Port $$port is already in use!$(RESET)"; \
			echo "  To free: sudo fuser -k $$port/tcp"; \
			echo "  Or kill previous MicroVM: pkill -f 'qemu.*hls-origin'"; \
			exit 1; \
		else \
			echo "$(GREEN)✓ Port $$port is available$(RESET)"; \
		fi; \
	done
	@echo ""

microvm-origin: microvm-check-kvm microvm-check-ports ## Run test origin as MicroVM (requires KVM)
	@echo "$(CYAN)Starting test origin MicroVM...$(RESET)"
	@echo ""
	@echo "$(GREEN)Stream URL:$(RESET) http://localhost:17080/stream.m3u8"
	@echo "$(GREEN)Health:$(RESET)     http://localhost:17080/health"
	@echo "$(GREEN)Metrics:$(RESET)    http://localhost:17113/metrics"
	@echo ""
	@echo "Press Ctrl+C to stop the VM"
	@echo ""
	$(NIX_RUN) .#test-origin-vm

microvm-origin-low-latency: microvm-check-kvm ## Run low-latency test origin as MicroVM
	@echo "$(CYAN)Starting low-latency test origin MicroVM...$(RESET)"
	$(NIX_RUN) .#test-origin-vm-low-latency

microvm-origin-stress: microvm-check-kvm ## Run stress-test origin as MicroVM
	@echo "$(CYAN)Starting stress-test origin MicroVM...$(RESET)"
	$(NIX_RUN) .#test-origin-vm-stress

microvm-origin-logged: microvm-check-kvm ## Run MicroVM with logging enabled (persistent logs)
	@echo "$(CYAN)Starting MicroVM with logging enabled...$(RESET)"
	@echo "$(GREEN)Logs will be saved to persistent volume$(RESET)"
	$(NIX_RUN) .#test-origin-vm-logged

microvm-origin-debug: microvm-check-kvm ## Run MicroVM with full debug logging
	@echo "$(CYAN)Starting MicroVM with full debug logging...$(RESET)"
	$(NIX_RUN) .#test-origin-vm-debug

microvm-start-tap: microvm-check-kvm ## Start MicroVM with TAP networking (recommended for load tests)
	@./scripts/microvm/start.sh --tap

microvm-origin-tap: microvm-check-kvm ## Run MicroVM with TAP networking (interactive, high perf)
	@echo "$(CYAN)Starting MicroVM with TAP networking (~10 Gbps)...$(RESET)"
	@echo "$(YELLOW)Ensure 'make network-setup' was run first!$(RESET)"
	$(NIX_RUN) .#test-origin-vm-tap

microvm-origin-tap-logged: microvm-check-kvm ## Run TAP MicroVM with logging enabled
	@echo "$(CYAN)Starting TAP MicroVM with logging...$(RESET)"
	$(NIX_RUN) .#test-origin-vm-tap-logged

# ============================================================================
# Load Tests (with local origin)
# Default: 30 second quick tests. Use DURATION=60s for longer tests.
# Example: make load-test-300 DURATION=5m
# ============================================================================

DURATION ?= 30s

load-test-50: build ## Run 50-client load test (gentle, 30s default)
	@./scripts/50-clients/run.sh $(DURATION)

load-test-100: build ## Run 100-client load test (standard, 30s default)
	@./scripts/100-clients/run.sh $(DURATION)

load-test-300: build ## Run 300-client load test (stress, 30s default)
	@./scripts/300-clients/run.sh $(DURATION)

load-test-500: build ## Run 500-client load test (heavy, 30s default)
	@./scripts/500-clients/run.sh $(DURATION)

load-test-1000: build ## Run 1000-client load test (extreme, 30s default)
	@./scripts/1000-clients/run.sh $(DURATION)

# Load tests against MicroVM (requires microvm-start first)
load-test-100-microvm: build ## Run 100-client test against MicroVM origin
	@echo "$(CYAN)Testing against MicroVM at http://localhost:17080$(RESET)"
	@./bin/go-ffmpeg-hls-swarm -clients 100 -duration $(DURATION) -ramp-rate 20 http://localhost:17080/stream.m3u8

load-test-300-microvm: build ## Run 300-client test against MicroVM origin
	@echo "$(CYAN)Testing against MicroVM at http://localhost:17080$(RESET)"
	@./bin/go-ffmpeg-hls-swarm -clients 300 -duration $(DURATION) -ramp-rate 50 http://localhost:17080/stream.m3u8

load-test-500-microvm: build ## Run 500-client test against MicroVM origin
	@echo "$(CYAN)Testing against MicroVM at http://localhost:17080$(RESET)"
	@./bin/go-ffmpeg-hls-swarm -clients 500 -duration $(DURATION) -ramp-rate 100 http://localhost:17080/stream.m3u8

# ============================================================================
# Network Setup (TAP + vhost-net for high-performance MicroVM networking)
# See: docs/MICROVM_NETWORKING.md
# ============================================================================

network-setup: ## Setup TAP + bridge networking for MicroVMs (requires sudo)
	@./scripts/network/setup.sh

network-teardown: ## Remove TAP + bridge networking
	@./scripts/network/teardown.sh

network-check: ## Verify network configuration is correct
	@./scripts/network/check.sh

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
	@echo "  logged         With buffered segment logging (512k buffer)"
	@echo "  debug          Full logging with gzip compression"
	@echo ""
	@echo "$(GREEN)Swarm Client Profiles:$(RESET)"
	@echo "  default        50 clients, 5/sec ramp"
	@echo "  stress         200 clients, 20/sec ramp"
	@echo "  gentle         20 clients, 1/sec ramp"
	@echo "  burst          100 clients, 50/sec ramp (thundering herd)"
	@echo "  extreme        500 clients, 50/sec ramp"
	@echo ""
	@echo "$(GREEN)Deployment Options:$(RESET)"
	@echo "  Runner script  Local dev (make test-origin)"
	@echo "  OCI Container  Docker/Podman (make container)"
	@echo "  MicroVM        KVM isolation (make microvm-origin)"
	@echo ""
	@echo "$(GREEN)Load Test Levels:$(RESET)"
	@echo "  50 clients     Gentle test (make load-test-50)"
	@echo "  100 clients    Standard test (make load-test-100)"
	@echo "  300 clients    Stress test (make load-test-300)"
	@echo "  500 clients    Heavy test (make load-test-500)"
	@echo "  1000 clients   Extreme test (make load-test-1000)"
	@echo ""
	@echo "$(GREEN)Documentation:$(RESET)"
	@echo "  docs/TEST_ORIGIN.md       Test HLS origin server"
	@echo "  docs/CLIENT_DEPLOYMENT.md Swarm client containers/VMs"
	@echo "  docs/DESIGN.md            Architecture overview"
