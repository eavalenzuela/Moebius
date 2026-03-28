# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Moebius is a FOSS device management platform (agent + server) written in Go. It manages Windows and Linux endpoints at scale using a poll-based architecture with mTLS authentication.

**Module:** `github.com/moebius-oss/moebius`

## Architecture

- **API Server (Go):** Single public-facing component. REST API with API key + OIDC auth, RBAC enforcement, job dispatch. Serves agent check-in endpoint.
- **Job Queue (NATS JetStream):** Decouples job creation from execution. Workers pull jobs and write results to PostgreSQL.
- **PostgreSQL:** Single source of truth. Tenant isolation via `tenant_id` on every table.
- **Agent (Go):** Polls server every 30s, ships heartbeat + delta inventory, receives and executes jobs. Runs as systemd service (Linux) or Windows Service. Includes local web UI (localhost-only) and CLI.
- **Web UI (React):** SPA that talks only to the API server.

Key invariants:
- Agents never receive inbound connections (poll-only)
- CDM (Customer Device Mode) is enforced on the agent, not the server
- All RBAC enforcement is in the API server; workers trust jobs are pre-authorized

## Monorepo Structure

```
agent/          # Agent binary: poller, executor, inventory, cdm, localui, localcli, platform/{windows,linux}
server/         # Server: api, auth, rbac, jobs, worker, store, notify
shared/         # protocol (agent<->server types), models, version
ui/             # React frontend
cli/            # Admin CLI (server-side)
deploy/         # docker-compose.yml, helm/, migrations/
```

## Design Specs

Design specs live in `docs/`. Key references when implementing:
- `docs/HIGH-LEVEL_DESIGN.md` — architecture, component responsibilities, directory layout
- `docs/AGENT_AUTH_SPEC.md` — mTLS enrollment, certificate lifecycle
- `docs/AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md` — check-in protocol, job lifecycle state machine
- `docs/REST_API_SPEC.md` — full API endpoint reference
- `docs/SERVER_DEPLOYMENT_SPEC.md` — Docker Compose (self-hosted) and Kubernetes/Helm (SaaS) deployment
- `docs/LOCAL_UI_CLI_SPEC.md` — agent local web UI and CLI interfaces
- `docs/FILE_TRANSFER_SPEC.md` — chunked file upload/download with signature verification
- `docs/AGENT_UPDATE_SPEC.md` — agent auto-update with rollback
- `docs/INSTALLER_PACKAGING_SPEC.md` — agent distribution and installation

Progress tracking at root: `FEATURE_REQUIREMENTS.md`, `IMPLEMENTATION_PLAN.md`.

## Build & Development

```bash
make build          # Build all binaries (native) to dist/
make test           # Run unit tests with race detector
make lint           # Run golangci-lint
make vet            # Run go vet
make fmt            # Format code
make fmt-check      # Check formatting (CI)
make test-cover     # Tests with coverage report
make build-agent-all  # Cross-compile agent for linux/{amd64,arm64}, windows/amd64
make build-server-all # Cross-compile server binaries for linux/{amd64,arm64}
make docker-build   # Build Docker images for api, worker, scheduler
make clean          # Remove build artifacts
```

Version is injected at build time via `-ldflags` from `shared/version` package.

**Four binaries:**
- `server/cmd/api` → `moebius-api` (subcommands: `migrate`, `generate-ca`, `create-admin`)
- `server/cmd/worker` → `moebius-worker`
- `server/cmd/scheduler` → `moebius-scheduler`
- `agent/cmd/agent` → `moebius-agent` (subcommands: `run`, `status`, `cdm`, `install`, `uninstall`, `verify`, `logs`, `version`)

**Stack:** Go, PostgreSQL, NATS JetStream. Migrations in `deploy/migrations/`. Docker images in `deploy/docker/`.
