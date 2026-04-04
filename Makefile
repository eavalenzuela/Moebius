export GOPATH := $(shell echo $${GOPATH:-$$HOME/go})
export PATH   := /usr/local/go/bin:$(GOPATH)/bin:$(PATH)

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

MODULE  = github.com/eavalenzuela/Moebius
LDFLAGS = -X $(MODULE)/shared/version.Version=$(VERSION) \
          -X $(MODULE)/shared/version.GitCommit=$(GIT_COMMIT) \
          -X $(MODULE)/shared/version.BuildTime=$(BUILD_TIME)

DIST = dist

# ─── Build ──────────────────────────────────────────────

.PHONY: build
build: build-api build-worker build-scheduler build-agent build-pkg-helper ## Build all binaries (native)

.PHONY: build-api
build-api: ## Build API server
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-api ./server/cmd/api

.PHONY: build-worker
build-worker: ## Build worker
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-worker ./server/cmd/worker

.PHONY: build-scheduler
build-scheduler: ## Build scheduler
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-scheduler ./server/cmd/scheduler

.PHONY: build-agent
build-agent: ## Build agent (native)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-agent ./agent/cmd/agent

.PHONY: build-pkg-helper
build-pkg-helper: ## Build setuid package helper (Linux only)
	GOOS=linux go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-pkg-helper ./agent/cmd/pkg-helper

# ─── Cross-compile agent ────────────────────────────────

.PHONY: build-agent-all
build-agent-all: build-agent-linux-amd64 build-agent-linux-arm64 build-agent-windows-amd64 build-pkg-helper-linux-amd64 build-pkg-helper-linux-arm64 ## Cross-compile agent for all platforms

.PHONY: build-agent-linux-amd64
build-agent-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-agent-linux-amd64 ./agent/cmd/agent

.PHONY: build-agent-linux-arm64
build-agent-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-agent-linux-arm64 ./agent/cmd/agent

.PHONY: build-agent-windows-amd64
build-agent-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-agent-windows-amd64.exe ./agent/cmd/agent

.PHONY: build-pkg-helper-linux-amd64
build-pkg-helper-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-pkg-helper-linux-amd64 ./agent/cmd/pkg-helper

.PHONY: build-pkg-helper-linux-arm64
build-pkg-helper-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-pkg-helper-linux-arm64 ./agent/cmd/pkg-helper

# ─── Installer packaging ───────────────────────────────

.PHONY: build-msi
build-msi: build-agent-windows-amd64 ## Build Windows MSI installer (requires WiX v4 + PowerShell)
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File agent/installer/wix/build.ps1 -Version $(VERSION) -BinaryPath $(DIST)/moebius-agent-windows-amd64.exe -OutputDir $(DIST)

.PHONY: build-tarball-linux-amd64
build-tarball-linux-amd64: build-agent-linux-amd64 build-pkg-helper-linux-amd64 ## Package Linux amd64 tarball
	@mkdir -p $(DIST)/tarball-linux-amd64
	cp $(DIST)/moebius-agent-linux-amd64 $(DIST)/tarball-linux-amd64/moebius-agent
	cp $(DIST)/moebius-pkg-helper-linux-amd64 $(DIST)/tarball-linux-amd64/moebius-pkg-helper
	cp deploy/install.sh deploy/uninstall.sh deploy/moebius-agent.service $(DIST)/tarball-linux-amd64/
	cd $(DIST) && tar czf agent-linux-amd64-$(VERSION).tar.gz -C tarball-linux-amd64 .
	rm -rf $(DIST)/tarball-linux-amd64

.PHONY: build-tarball-linux-arm64
build-tarball-linux-arm64: build-agent-linux-arm64 build-pkg-helper-linux-arm64 ## Package Linux arm64 tarball
	@mkdir -p $(DIST)/tarball-linux-arm64
	cp $(DIST)/moebius-agent-linux-arm64 $(DIST)/tarball-linux-arm64/moebius-agent
	cp $(DIST)/moebius-pkg-helper-linux-arm64 $(DIST)/tarball-linux-arm64/moebius-pkg-helper
	cp deploy/install.sh deploy/uninstall.sh deploy/moebius-agent.service $(DIST)/tarball-linux-arm64/
	cd $(DIST) && tar czf agent-linux-arm64-$(VERSION).tar.gz -C tarball-linux-arm64 .
	rm -rf $(DIST)/tarball-linux-arm64

.PHONY: dist
dist: build-tarball-linux-amd64 build-tarball-linux-arm64 ## Build all release artifacts (Linux tarballs; MSI requires Windows)
	@echo "Linux tarballs built. Run 'make build-msi' on Windows for MSI."

# ─── Cross-compile server ───────────────────────────────

.PHONY: build-server-all
build-server-all: ## Cross-compile all server binaries for linux/amd64 and linux/arm64
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-api-linux-amd64 ./server/cmd/api
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-api-linux-arm64 ./server/cmd/api
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-worker-linux-amd64 ./server/cmd/worker
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-worker-linux-arm64 ./server/cmd/worker
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-scheduler-linux-amd64 ./server/cmd/scheduler
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/moebius-scheduler-linux-arm64 ./server/cmd/scheduler

# ─── Test & Lint ────────────────────────────────────────

.PHONY: test
test: ## Run unit tests
	go test -race -count=1 ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage
	go test -race -count=1 -coverprofile=coverage.txt ./...
	go tool cover -func=coverage.txt

.PHONY: test-integration
test-integration: ## Run integration tests (requires running services)
	go test -race -count=1 -tags=integration ./...

.PHONY: lint
lint: ## Run linter
	golangci-lint run ./agent/... ./server/... ./shared/... ./tools/... ./tests/...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format code
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Check formatting (CI)
	@test -z "$$(gofmt -s -l . | tee /dev/stderr)" || (echo "Run 'make fmt' to fix" && exit 1)

# ─── Database ───────────────────────────────────────────

.PHONY: migrate
migrate: ## Run database migrations
	$(DIST)/moebius-api migrate

# ─── Docker ─────────────────────────────────────────────

DOCKER_REGISTRY ?= ghcr.io/eavalenzuela
DOCKER_TAG      ?= $(VERSION)

.PHONY: docker-build
docker-build: docker-build-api docker-build-worker docker-build-scheduler ## Build all Docker images

.PHONY: docker-build-api
docker-build-api:
	docker build -f deploy/docker/Dockerfile.api -t $(DOCKER_REGISTRY)/moebius-api:$(DOCKER_TAG) .

.PHONY: docker-build-worker
docker-build-worker:
	docker build -f deploy/docker/Dockerfile.worker -t $(DOCKER_REGISTRY)/moebius-worker:$(DOCKER_TAG) .

.PHONY: docker-build-scheduler
docker-build-scheduler:
	docker build -f deploy/docker/Dockerfile.scheduler -t $(DOCKER_REGISTRY)/moebius-scheduler:$(DOCKER_TAG) .

.PHONY: docker-push
docker-push: ## Push all Docker images
	docker push $(DOCKER_REGISTRY)/moebius-api:$(DOCKER_TAG)
	docker push $(DOCKER_REGISTRY)/moebius-worker:$(DOCKER_TAG)
	docker push $(DOCKER_REGISTRY)/moebius-scheduler:$(DOCKER_TAG)

# ─── Clean ──────────────────────────────────────────────

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(DIST) coverage.txt coverage.html

# ─── Help ───────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-24s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
