# Server Deployment Specification

---

## Overview

The server is composed of three distinct processes — API server, worker, and scheduler — backed by PostgreSQL and NATS JetStream. Two official deployment targets are supported: Docker Compose for self-hosted deployments, and Kubernetes (Helm) for SaaS. Both targets run the same container images with different configuration and scaling profiles.

---

## Components

### Process Decomposition

```
┌─────────────────────────────────────────────────────────────┐
│                        Ingress Layer                         │
│         Reverse Proxy (self-hosted) / Cloud LB (SaaS)       │
│                     TLS termination                          │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTP (internal)
┌───────────────────────────▼─────────────────────────────────┐
│                        API Server                            │
│  - REST API (all external traffic)                          │
│  - Agent check-in + enrollment                              │
│  - mTLS validation                                          │
│  - RBAC enforcement                                         │
│  - Job creation → NATS JetStream                            │
│  - File upload handling                                     │
└──────────────┬──────────────────────────┬───────────────────┘
               │                          │
               │ reads/writes             │ publishes jobs
┌──────────────▼──────┐      ┌────────────▼────────────────────┐
│     PostgreSQL       │      │        NATS JetStream           │
│                      │      │                                 │
│  - All persistent    │      │  Streams:                       │
│    state             │      │  - jobs (job dispatch)          │
│  - Source of truth   │      │  - results (job results)        │
│                      │      │  - logs (agent log shipping)    │
└──────────────▲──────┘      └────────────┬────────────────────┘
               │                          │ consumes
               │ reads/writes  ┌──────────▼────────────────────┐
               │               │           Worker               │
               └───────────────│  - Pulls jobs from NATS       │
                               │  - Manages job state machine  │
                               │  - Writes results to PG       │
                               │  - Handles CDM hold logic     │
                               │  - Scalable horizontally      │
                               └───────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                        Scheduler                             │
│  - Evaluates scheduled jobs (cron)                          │
│  - Evaluates auto-update policies                           │
│  - Enqueues jobs → NATS JetStream                           │
│  - Manages gradual rollout batching                         │
│  - Sends alerts + webhooks                                  │
│  - Runs as a single instance (leader-elected)               │
└─────────────────────────────────────────────────────────────┘
```

### API Server
- Handles all inbound traffic: REST API, agent check-in, agent enrollment
- Validates mTLS client certificates for agent endpoints
- Enforces RBAC and tenant scoping on every request
- Publishes new jobs to NATS JetStream; does not execute jobs directly
- Handles chunked file uploads; writes completed files to storage backend
- Stateless — run as many replicas as needed behind the load balancer

### Worker
- Consumes jobs from NATS JetStream
- Manages the job state machine (QUEUED → DISPATCHED → ACKNOWLEDGED → RUNNING → terminal)
- On agent check-in: determines which jobs to include in the response based on device CDM state
- Writes job state transitions and results to PostgreSQL
- Stateless — run as many replicas as needed; NATS handles work distribution
- Each worker instance processes jobs concurrently with a configurable goroutine pool

### Scheduler
- Single logical instance — uses PostgreSQL advisory locks for leader election (only one scheduler runs at a time across all replicas)
- Evaluates cron expressions for scheduled jobs and enqueues them when due
- Evaluates auto-update policies when new agent versions are published
- Manages gradual rollout batching — tracks batch progress and enqueues subsequent batches
- Monitors device last-seen timestamps and fires agent-offline alert rules
- Sends webhook and email notifications for alert rule triggers
- Lightweight — one replica is sufficient; a second can stand by for failover

---

## NATS JetStream Streams

| Stream | Subjects | Retention | Consumers |
|---|---|---|---|
| `jobs` | `jobs.dispatch.{tenant_id}.{device_id}` | Work queue (deleted on ack) | Worker pool (push consumer) |
| `results` | `results.{tenant_id}.{job_id}` | Interest (deleted after processing) | Worker (processes and writes to PG) |
| `logs` | `logs.{tenant_id}.{device_id}` | Limits (max age: 7 days) | Worker (writes to PG log store) |

- NATS JetStream persistence ensures no jobs are lost if all workers restart simultaneously
- Work queue semantics on `jobs` stream ensure each job is delivered to exactly one worker
- NATS cluster (3 nodes) is recommended for production SaaS; single node is acceptable for self-hosted

---

## Self-Hosted: Docker Compose

### Target Audience
Operators running the server on a single machine or small VM. Designed for simplicity — one command to start, minimal configuration required.

### Compose Services

```yaml
services:

  postgres:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_DB: agent_server
      POSTGRES_USER: agent
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U agent"]
      interval: 10s
      timeout: 5s
      retries: 5

  nats:
    image: nats:2-alpine
    restart: unless-stopped
    command: ["-js", "-sd", "/data"]
    volumes:
      - nats_data:/data

  api:
    image: ghcr.io/your-org/agent-server-api:${VERSION:-latest}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      nats:
        condition: service_started
    environment:
      DATABASE_URL: postgres://agent:${POSTGRES_PASSWORD}@postgres:5432/agent_server
      NATS_URL: nats://nats:4222
      HTTP_PORT: 8080
      TLS_MODE: passthrough  # TLS terminated by proxy
      CA_KEY_PATH: /certs/ca.key
      CA_CERT_PATH: /certs/ca.crt
      STORAGE_BACKEND: local
      STORAGE_PATH: /data/files
    volumes:
      - ./certs:/certs:ro
      - file_data:/data/files
    ports:
      - "127.0.0.1:8080:8080"  # Exposed to proxy only

  worker:
    image: ghcr.io/your-org/agent-server-worker:${VERSION:-latest}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      nats:
        condition: service_started
    environment:
      DATABASE_URL: postgres://agent:${POSTGRES_PASSWORD}@postgres:5432/agent_server
      NATS_URL: nats://nats:4222
      WORKER_CONCURRENCY: 20

  scheduler:
    image: ghcr.io/your-org/agent-server-scheduler:${VERSION:-latest}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      nats:
        condition: service_started
    environment:
      DATABASE_URL: postgres://agent:${POSTGRES_PASSWORD}@postgres:5432/agent_server
      NATS_URL: nats://nats:4222

  proxy:
    image: caddy:2-alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      - api

volumes:
  postgres_data:
  nats_data:
  file_data:
  caddy_data:
  caddy_config:
```

### Caddyfile (Self-Hosted)

```
{$SERVER_DOMAIN} {
    reverse_proxy api:8080
}
```

Caddy handles automatic TLS via Let's Encrypt. The operator sets `SERVER_DOMAIN` in `.env`.

### Self-Hosted Configuration

Operators configure the server via a `.env` file:

```env
# Required
SERVER_DOMAIN=manage.example.com
POSTGRES_PASSWORD=changeme

# Optional overrides
VERSION=1.5.0
WORKER_CONCURRENCY=20
STORAGE_BACKEND=local   # or 's3'

# S3 storage (if STORAGE_BACKEND=s3)
S3_ENDPOINT=https://s3.amazonaws.com
S3_BUCKET=my-agent-files
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
```

### Self-Hosted Startup Sequence

```bash
# 1. Clone or download deployment files
git clone https://github.com/your-org/agent-server-deploy

# 2. Configure environment
cp .env.example .env
# Edit .env with your domain and passwords

# 3. Generate CA (first run only)
docker compose run --rm api generate-ca

# 4. Run database migrations
docker compose run --rm api migrate

# 5. Create initial admin user
docker compose run --rm api create-admin --email admin@example.com

# 6. Start all services
docker compose up -d
```

### Upgrading (Self-Hosted)

```bash
# Pull new images
docker compose pull

# Run migrations (safe to run on every upgrade)
docker compose run --rm api migrate

# Restart services with zero-downtime rolling restart
docker compose up -d --no-deps api
docker compose up -d --no-deps worker
docker compose up -d --no-deps scheduler
```

---

## SaaS: Kubernetes / Helm

### Target Audience
Operators running the platform as a multi-tenant SaaS. Designed for horizontal scalability, high availability, and operational observability.

### Helm Chart Structure

```
charts/agent-server/
├── Chart.yaml
├── values.yaml
├── values.production.yaml
└── templates/
    ├── api/
    │   ├── deployment.yaml
    │   ├── service.yaml
    │   ├── hpa.yaml
    │   └── pdb.yaml
    ├── worker/
    │   ├── deployment.yaml
    │   ├── hpa.yaml
    │   └── pdb.yaml
    ├── scheduler/
    │   └── deployment.yaml
    ├── nats/
    │   └── statefulset.yaml  (or external NATS via values)
    ├── migrations/
    │   └── job.yaml
    ├── ingress.yaml
    ├── configmap.yaml
    └── secrets.yaml
```

### Key values.yaml Sections

```yaml
api:
  replicaCount: 3
  image:
    repository: ghcr.io/your-org/agent-server-api
    tag: ""  # defaults to chart appVersion
  resources:
    requests:
      cpu: 250m
      memory: 256Mi
    limits:
      cpu: 1000m
      memory: 512Mi
  autoscaling:
    enabled: true
    minReplicas: 3
    maxReplicas: 20
    targetCPUUtilizationPercentage: 70
  podDisruptionBudget:
    minAvailable: 2

worker:
  replicaCount: 3
  concurrency: 50
  autoscaling:
    enabled: true
    minReplicas: 3
    maxReplicas: 50
    targetCPUUtilizationPercentage: 70

scheduler:
  replicaCount: 2  # one active, one standby (leader election via PG advisory lock)

nats:
  external: false  # set true to use an external NATS cluster
  externalUrl: ""
  cluster:
    enabled: true
    replicas: 3

postgres:
  external: true   # always use external managed PG in SaaS (RDS, Cloud SQL, etc.)
  url: ""          # set via secret

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls: true

storage:
  backend: s3      # always S3-compatible in SaaS
  s3:
    endpoint: ""
    bucket: ""
    region: ""
    credentialsSecret: agent-s3-credentials
```

### Ingress and TLS (SaaS)

In SaaS deployments, TLS is terminated at the cloud load balancer or ingress controller. The API server receives plain HTTP internally. cert-manager handles certificate provisioning and renewal.

```
Internet
  │
  ▼
Cloud Load Balancer (TLS termination)
  │
  ▼
Kubernetes Ingress (nginx)
  │
  ▼
API Server Service (ClusterIP)
  │
  ▼
API Server Pods
```

### mTLS for Agent Endpoints (SaaS)

Agent endpoints require mTLS — the agent's client certificate must be validated. Since TLS is terminated at the load balancer, one of the following approaches is required:

**Option A (Recommended): mTLS passthrough for agent endpoints**
Configure the load balancer to pass TLS directly to the API server for agent endpoint paths (`/v1/agents/*`). The API server handles TLS for these paths only, terminating and validating client certificates itself. All other paths are served via the standard ingress.

**Option B: Forward client certificate headers**
The load balancer terminates TLS, extracts the client certificate, and forwards it as an `X-Client-Cert` header. The API server validates the certificate from the header. This is simpler to configure but requires trusting the load balancer completely and careful header sanitization to prevent spoofing.

Option A is preferred for its stronger security properties.

---

## Database Migrations

Migrations are managed with a dedicated migration tool (e.g. `golang-migrate`). Migration files live in `deploy/migrations/`.

- Migrations are always forward-only — no down migrations in production
- Each migration is a numbered SQL file: `0001_initial_schema.sql`, `0002_add_sites.sql`, etc.
- The `migrate` command is idempotent — safe to run on every deployment
- In Kubernetes, migrations run as a pre-upgrade Helm hook Job before new pods start
- In Docker Compose, migrations are run manually before `docker compose up`

---

## Environment Variables Reference

All three server processes share a common set of environment variables:

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | Yes | PostgreSQL connection string |
| `NATS_URL` | Yes | NATS server URL |
| `LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |
| `LOG_FORMAT` | No | `json` or `text` (default: `json`) |
| `TENANT_MODE` | No | `single` or `multi` (default: `multi`) |

**API server only:**

| Variable | Required | Description |
|---|---|---|
| `HTTP_PORT` | No | Port to listen on (default: `8080`) |
| `TLS_MODE` | No | `passthrough` (proxy handles TLS) or `direct` (API handles TLS) |
| `TLS_CERT_PATH` | If TLS_MODE=direct | Path to server TLS certificate |
| `TLS_KEY_PATH` | If TLS_MODE=direct | Path to server TLS key |
| `CA_CERT_PATH` | Yes | Path to intermediate CA certificate (for mTLS validation) |
| `CA_KEY_PATH` | Yes | Path to intermediate CA private key (for signing agent CSRs) |
| `STORAGE_BACKEND` | No | `local` or `s3` (default: `local`) |
| `STORAGE_PATH` | If local | Local filesystem path for file storage |
| `S3_ENDPOINT` | If s3 | S3-compatible endpoint URL |
| `S3_BUCKET` | If s3 | S3 bucket name |
| `S3_REGION` | If s3 | S3 region |
| `S3_ACCESS_KEY_ID` | If s3 | S3 access key |
| `S3_SECRET_ACCESS_KEY` | If s3 | S3 secret key |
| `OIDC_ISSUER_URL` | No | OIDC provider issuer URL for SSO |
| `OIDC_CLIENT_ID` | No | OIDC client ID |
| `OIDC_CLIENT_SECRET` | No | OIDC client secret |

**Worker only:**

| Variable | Required | Description |
|---|---|---|
| `WORKER_CONCURRENCY` | No | Max concurrent jobs per worker instance (default: `20`) |

---

## Observability

### Logging
All processes emit structured JSON logs to stdout. Log aggregation is the operator's responsibility (Loki, CloudWatch, Datadog, etc.).

Log fields always include: `timestamp`, `level`, `service`, `version`, `request_id` (where applicable), `tenant_id` (where applicable).

### Metrics
All processes expose a Prometheus metrics endpoint at `GET /metrics` (not proxied externally).

Key metrics exposed:

| Metric | Type | Description |
|---|---|---|
| `agent_checkins_total` | Counter | Total check-ins received |
| `agent_online` | Gauge | Currently online agents (by tenant) |
| `jobs_enqueued_total` | Counter | Jobs enqueued (by type, tenant) |
| `jobs_completed_total` | Counter | Jobs completed (by type, status, tenant) |
| `job_duration_seconds` | Histogram | Job execution duration (by type) |
| `job_queue_depth` | Gauge | Current NATS queue depth (by stream) |
| `file_transfer_bytes_total` | Counter | Bytes transferred (by direction, tenant) |
| `api_request_duration_seconds` | Histogram | API request latency (by endpoint, status) |
| `db_query_duration_seconds` | Histogram | Database query latency (by query) |

### Health Endpoints

All processes expose:
```
GET /health        ← liveness probe (returns 200 if process is running)
GET /health/ready  ← readiness probe (returns 200 if DB + NATS connections are healthy)
```

---

## Security Boundaries

| Concern | Mitigation |
|---|---|
| Database not exposed externally | PostgreSQL is internal-only; no external port |
| NATS not exposed externally | NATS is internal-only; no external port |
| CA private key protection | Stored in a protected volume (self-hosted) or Kubernetes Secret with restricted RBAC (SaaS) |
| Worker/scheduler have no public surface | Only the API server is exposed via ingress |
| Secret management | `.env` file for self-hosted; Kubernetes Secrets + external secret operator (e.g. External Secrets Operator) for SaaS |
| Image provenance | All images signed with cosign and published to GHCR; Helm chart verifies digests |

---

## Monorepo Build Targets

Each server process is a separate Go binary built from the monorepo:

```
go build ./server/cmd/api        → agent-server-api
go build ./server/cmd/worker     → agent-server-worker
go build ./server/cmd/scheduler  → agent-server-scheduler
```

Each binary is packaged into its own minimal container image (distroless or alpine base). All three share the same image tag per release — versions are always deployed in lockstep.

GitHub Actions matrix build produces images for `linux/amd64` and `linux/arm64`.
