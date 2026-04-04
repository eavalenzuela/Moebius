# Moebius

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/eavalenzuela/Moebius/actions/workflows/ci.yml/badge.svg)](https://github.com/eavalenzuela/Moebius/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/eavalenzuela/Moebius)](go.mod)

Moebius is a free and open-source device management platform for managing Windows and Linux endpoints at scale. It consists of a lightweight agent that runs on managed devices and a server stack that provides a REST API, job execution engine, and web UI.

Agents use a **poll-based architecture** — they check in with the server on a regular interval, receive jobs, and report results. There are no inbound connections to managed devices. All agent-to-server communication is authenticated with **mTLS certificates** issued during enrollment.

The server supports **multi-tenancy** with full data isolation, **role-based access control** with predefined and custom roles, and **Customer Device Mode (CDM)** which gives end users control over when management actions execute on their machines.

## Architecture

```
                    ┌──────────────┐
                    │   Web UI     │
                    │   (React)    │
                    └──────┬───────┘
                           │ REST API
                    ┌──────▼───────┐        ┌──────────────┐
  Agents ─────────► │  API Server  │◄──────►│  PostgreSQL  │
  (poll over mTLS)  │  (Go/Chi)   │        │              │
                    └──────┬───────┘        └──────▲───────┘
                           │                       │
                    ┌──────▼───────┐        ┌──────┴───────┐
                    │     NATS     │◄──────►│   Worker(s)  │
                    │  JetStream   │        │              │
                    └──────┬───────┘        └──────────────┘
                           │
                    ┌──────▼───────┐
                    │  Scheduler   │
                    │  (cron/alerts)│
                    └──────────────┘
```

**Components:**

| Component | Description |
|-----------|-------------|
| **API Server** | REST API with API key + OIDC auth, RBAC enforcement, agent check-in endpoint, job dispatch |
| **Worker** | Pulls jobs from NATS JetStream, executes, writes results to PostgreSQL |
| **Scheduler** | Evaluates cron schedules and alert rules, sends notifications |
| **Agent** | Runs on managed endpoints as a system service; polls for jobs, ships inventory and logs |
| **Web UI** | React SPA for device management, job creation, and monitoring |

## Features

- **Agent enrollment** with single-use tokens and automatic mTLS certificate provisioning
- **Job execution** — run commands, install/remove/update packages, transfer files, update agents
- **Inventory collection** — hardware and package inventory with delta reporting
- **File transfer** — chunked upload with SHA-256 verification and Ed25519 signatures
- **Agent self-update** — versioned binary downloads with signature verification and automatic rollback
- **Customer Device Mode (CDM)** — end-user controlled sessions that gate when jobs execute
- **Scheduled jobs** — cron-based recurring job dispatch
- **Alert rules** — device offline detection, version drift, and custom conditions with email/webhook notifications
- **Multi-tenancy** — full data isolation with per-tenant configuration
- **RBAC** — Super Admin, Tenant Admin, Operator, Technician, Viewer roles + custom roles
- **Audit logging** — append-only log of all administrative actions
- **Certificate lifecycle** — automatic renewal, revocation, and expired cert handling
- **Cross-platform agent** — Linux (systemd) and Windows (service) with amd64 and arm64 support

## Quick Start

### Docker Compose (recommended)

```bash
cd deploy
cp .env.example .env
# Edit .env: set SERVER_DOMAIN and POSTGRES_PASSWORD

docker compose run --rm api generate-ca
docker compose run --rm api migrate
docker compose run --rm api create-admin --email admin@example.com
docker compose up -d
```

### Build from Source

```bash
# Prerequisites: Go 1.25+, libpam0g-dev (Linux), PostgreSQL, NATS
make build                # Build all binaries to dist/
make test                 # Run unit tests
make lint                 # Run golangci-lint
make test-integration     # Integration tests (requires postgres + nats)
```

See [Deployment Instructions](docs/Deployment_Instructions.md) for full setup guides covering local development, Docker Compose, and Kubernetes/Helm.

## Documentation

| Document | Description |
|----------|-------------|
| [Deployment Instructions](docs/Deployment_Instructions.md) | Local dev, Docker Compose, Kubernetes/Helm setup, upgrades, env vars |
| [User Guide](docs/User_Guide.md) | Operator guide: enrollment, devices, jobs, CDM, alerts, RBAC |
| [REST API Spec](docs/REST_API_SPEC.md) | Full API endpoint reference with request/response examples |
| [Security](SECURITY.md) | Security architecture and design decisions |
| [High-Level Design](docs/HIGH-LEVEL_DESIGN.md) | Architecture, component responsibilities, data flow |
| [Agent Auth Spec](docs/AGENT_AUTH_SPEC.md) | mTLS enrollment, certificate lifecycle |
| [Check-in & Core Design](docs/AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md) | Agent protocol, job state machine |
| [File Transfer Spec](docs/FILE_TRANSFER_SPEC.md) | Chunked upload, signature verification |
| [Agent Update Spec](docs/AGENT_UPDATE_SPEC.md) | Binary update, rollback, version management |
| [Local UI/CLI Spec](docs/LOCAL_UI_CLI_SPEC.md) | Agent local web UI and CLI |
| [Installer Packaging Spec](docs/INSTALLER_PACKAGING_SPEC.md) | Agent distribution, MSI/tarball, release signing |
| [Server Deployment Spec](docs/SERVER_DEPLOYMENT_SPEC.md) | Infrastructure design, NATS streams, observability |

## Project Structure

```
agent/          # Agent: poller, executor, inventory, CDM, local UI/CLI
server/         # Server: API, auth, RBAC, jobs, worker, scheduler, store
shared/         # Shared: protocol types, models, version
ui/             # React frontend (Vite + TypeScript)
deploy/         # Docker Compose, Helm chart, migrations, install scripts
docs/           # Design specs and guides
tools/          # Release signing utilities (keygen, sign)
tests/          # Integration tests
```

## Build Targets

```bash
make build              # Build all binaries (native)
make build-agent-all    # Cross-compile agent: linux/{amd64,arm64}, windows/amd64
make build-server-all   # Cross-compile server: linux/{amd64,arm64}
make docker-build       # Build Docker images (api, worker, scheduler)
make dist               # Build release tarballs
make test               # Unit tests with race detector
make test-integration   # Integration tests (postgres + nats required)
make test-cover         # Tests with coverage report
make lint               # golangci-lint
make fmt                # Format code
make clean              # Remove build artifacts
```

## License

[MIT](LICENSE) - Copyright (c) 2026 Eric V

<details>
<summary>Implementation Progress</summary>

All 20 phases complete. See [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) for detailed task tracking.

- [x] Phase 0 — Project Scaffolding
- [x] Phase 1 — Schema & Shared Types
- [x] Phase 2 — Server Core
- [x] Phase 3 — PKI & Agent Enrollment
- [x] Phase 4 — API Server: Auth & RBAC
- [x] Phase 5 — Agent Core
- [x] Phase 6 — Job Lifecycle
- [x] Phase 7 — Device Management
- [x] Phase 8 — Inventory Collection
- [x] Phase 9 — CDM (Customer Device Mode)
- [x] Phase 10 — Log Shipping
- [x] Phase 11 — File Transfer
- [x] Phase 12 — Package Management Jobs
- [x] Phase 13 — Agent Update
- [x] Phase 14 — Scheduling & Alerting
- [x] Phase 15 — Agent Local Interfaces
- [x] Phase 16 — Installer & Packaging
- [x] Phase 17 — Deployment Configs
- [x] Phase 18 — Web UI (React)
- [x] Phase 19 — Release Pipeline
- [x] Phase 20 — Integration Testing & Hardening

</details>
