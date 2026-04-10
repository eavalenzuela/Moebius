# Deployment Instructions

This document covers how to build, deploy, and operate the Moebius platform across all supported deployment methods.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Architecture Overview](#architecture-overview)
- [Local Development](#local-development)
- [Docker Compose (Self-Hosted)](#docker-compose-self-hosted)
- [Kubernetes / Helm (SaaS)](#kubernetes--helm-saas)
- [Agent Installation](#agent-installation)
- [Release Signing](#release-signing)
- [Release Pipeline (CI/CD)](#release-pipeline-cicd)
- [Database Migrations](#database-migrations)
- [Database TLS](#database-tls)
- [Environment Variable Reference](#environment-variable-reference)
- [Upgrading](#upgrading)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

**Build tools:**

- Go 1.25+
- Docker with Buildx (for container builds)
- `libpam0g-dev` (Linux, for building the agent)
- GNU Make

**Runtime dependencies:**

- PostgreSQL 16+

**Optional:**

- WiX v4 + PowerShell (for Windows MSI builds, Windows host only)
- cosign (for container image signing)
- golangci-lint (for linting)
- Helm 3 (for Kubernetes deployment)

---

## Architecture Overview

Moebius consists of four components:

| Component     | Binary              | Description                                                              |
|---------------|---------------------|--------------------------------------------------------------------------|
| API Server    | `moebius-api`       | REST API, agent check-in endpoint, RBAC enforcement, inline job dispatch |
| Scheduler     | `moebius-scheduler` | Cron evaluation, alert rule checks, SMTP notifications, job/token reaper |
| Agent         | `moebius-agent`     | Runs on managed endpoints, polls server for jobs                         |
| Pkg Helper    | `moebius-pkg-helper`| Setuid helper for package operations on Linux agents                     |

Supporting infrastructure: PostgreSQL (data), Caddy (TLS proxy in self-hosted mode).

Job dispatch is inline — the API check-in handler hands queued jobs to agents as they poll. There is no message bus. The scheduler runs cron-scheduled jobs, fires alerts, and reaps stuck jobs + expired enrollment tokens.

---

## Local Development

### 1. Start Infrastructure

Run PostgreSQL locally. Using Docker:

```bash
docker run -d --name moebius-postgres \
  -e POSTGRES_DB=moebius \
  -e POSTGRES_USER=moebius \
  -e POSTGRES_PASSWORD=devpass \
  -p 5432:5432 \
  postgres:16
```

### 2. Build All Binaries

```bash
sudo apt-get install -y libpam0g-dev   # Linux only
make build
```

This produces binaries in `dist/`:
- `moebius-api`
- `moebius-scheduler`
- `moebius-agent`
- `moebius-pkg-helper`

### 3. Run Database Migrations

```bash
DATABASE_URL="postgres://moebius:devpass@localhost:5432/moebius?sslmode=disable" \
  ./dist/moebius-api migrate
```

### 4. Generate Certificate Authority

The API server requires a CA keypair for agent mTLS enrollment:

```bash
DATABASE_URL="postgres://moebius:devpass@localhost:5432/moebius?sslmode=disable" \
  ./dist/moebius-api generate-ca
```

This writes `intermediate-ca.key` and `intermediate-ca.crt` to the current directory.

### 5. Bootstrap Admin User

```bash
DATABASE_URL="postgres://moebius:devpass@localhost:5432/moebius?sslmode=disable" \
  ./dist/moebius-api create-admin --email admin@localhost
```

This prints a one-time API key (`sk_...`). Save it — it cannot be retrieved later.

### 6. Start Server Components

Open two terminals (or use a process manager):

```bash
# Terminal 1 — API Server
export DATABASE_URL="postgres://moebius:devpass@localhost:5432/moebius?sslmode=disable"
export CA_CERT_PATH="./intermediate-ca.crt"
export CA_KEY_PATH="./intermediate-ca.key"
export TLS_MODE=passthrough
export STORAGE_BACKEND=local
export STORAGE_PATH=/tmp/moebius-files
export LOG_FORMAT=text
./dist/moebius-api

# Terminal 2 — Scheduler
export DATABASE_URL="postgres://moebius:devpass@localhost:5432/moebius?sslmode=disable"
export LOG_FORMAT=text
./dist/moebius-scheduler
```

The API server listens on `http://localhost:8080` by default.

### 7. Run the Agent Locally (Optional)

```bash
./dist/moebius-agent run \
  --server-url http://localhost:8080 \
  --enrollment-token <token-from-api>
```

### Build Targets Reference

```bash
make build              # Build all binaries (native)
make build-agent-all    # Cross-compile agent for linux/{amd64,arm64}, windows/amd64
make build-server-all   # Cross-compile server for linux/{amd64,arm64}
make test               # Unit tests with race detector
make test-integration   # Integration tests (requires postgres)
make test-cover         # Tests with coverage report
make lint               # golangci-lint
make vet                # go vet
make fmt                # Format code
make fmt-check          # Check formatting (CI)
make clean              # Remove dist/ and coverage files
```

---

## Docker Compose (Self-Hosted)

The Docker Compose stack runs the full platform on a single host with automatic TLS via Caddy.

### Directory Layout

```
deploy/
├── docker-compose.yml      # Service definitions
├── .env.example            # Environment template
├── Caddyfile               # Reverse proxy config
├── docker/
│   ├── Dockerfile.api
│   └── Dockerfile.scheduler
└── migrations/             # SQL migration files
```

### 1. Configure Environment

```bash
cd deploy
cp .env.example .env
```

Edit `.env` — two values are **required**:

| Variable           | Description                         |
|--------------------|-------------------------------------|
| `SERVER_DOMAIN`    | Public FQDN (e.g. `manage.example.com`) |
| `POSTGRES_PASSWORD`| PostgreSQL password                  |

Optional overrides: `VERSION`, `LOG_LEVEL`, `LOG_FORMAT`, `WORKER_CONCURRENCY`, S3 storage, and SMTP settings. See [Environment Variable Reference](#environment-variable-reference).

### 2. Generate CA Keypair

```bash
docker compose run --rm api generate-ca
```

This writes the CA cert and key into the `certs/` directory mounted by the API container. If the directory doesn't exist, create it:

```bash
mkdir -p certs
```

Move the generated files into `certs/` if they were written to the container's working directory.

### 3. Run Database Migrations

```bash
docker compose run --rm api migrate
```

### 4. Create Admin User

```bash
docker compose run --rm api create-admin --email admin@example.com
```

Save the printed API key — it is shown only once.

### 5. Start the Stack

```bash
docker compose up -d
```

Services started:
- **postgres** — Database (port 5432, internal only)
- **api** — API server (port 8080, exposed on localhost only)
- **scheduler** — Cron, alert evaluation, and reaper
- **proxy** — Caddy reverse proxy (ports 80/443, public)

Caddy automatically obtains a TLS certificate from Let's Encrypt for `SERVER_DOMAIN`.

### 6. Verify

```bash
# Check all services are running
docker compose ps

# Check health endpoint
curl -s https://<SERVER_DOMAIN>/health | jq .

# Check readiness
curl -s https://<SERVER_DOMAIN>/health/ready | jq .
```

### Persistent Volumes

| Volume          | Purpose                           |
|-----------------|-----------------------------------|
| `postgres_data` | PostgreSQL database files         |
| `file_data`     | Uploaded files (agent packages, transfers) |
| `caddy_data`    | TLS certificates and ACME state   |
| `caddy_config`  | Caddy runtime config              |

### Building Images Locally

If you prefer building images from source instead of pulling from GHCR:

```bash
make docker-build                     # Build all images (api, scheduler)
make docker-build DOCKER_TAG=v1.2.3   # With custom tag
```

---

## Kubernetes / Helm (SaaS)

The Helm chart deploys the API and scheduler with autoscaling, PDBs, and ingress.

### Prerequisites

- Kubernetes 1.26+
- Helm 3
- An external PostgreSQL instance (managed service recommended)
- cert-manager installed (for TLS certificates)
- nginx ingress controller (or modify `ingress.className`)
- S3-compatible object storage

### Directory Layout

```
deploy/helm/charts/moebius/
├── Chart.yaml
├── values.yaml                  # Default/dev values
├── values.production.yaml       # Production overrides
└── templates/
    ├── _helpers.tpl
    ├── configmap.yaml
    ├── secrets.yaml
    ├── ingress.yaml
    ├── api/
    │   ├── deployment.yaml
    │   ├── service.yaml
    │   ├── hpa.yaml
    │   └── pdb.yaml
    ├── scheduler/
    │   └── deployment.yaml
    └── migrations/
        └── job.yaml             # Pre-upgrade hook
```

### 1. Create Secrets

```bash
# Database connection string
kubectl create secret generic moebius-db \
  --from-literal=DATABASE_URL="postgres://moebius:PASSWORD@db-host:5432/moebius?sslmode=require"

# CA certificate and key for agent mTLS enrollment
kubectl create secret tls moebius-ca-certs \
  --cert=intermediate-ca.crt \
  --key=intermediate-ca.key

# S3 credentials
kubectl create secret generic moebius-s3-credentials \
  --from-literal=S3_ACCESS_KEY_ID="..." \
  --from-literal=S3_SECRET_ACCESS_KEY="..."
```

### 2. Configure Values

Create a values override file for your environment:

```yaml
# my-values.yaml
ingress:
  host: manage.yourcompany.com

storage:
  s3:
    endpoint: https://s3.us-east-1.amazonaws.com
    bucket: moebius-files
    region: us-east-1

secrets:
  databaseUrl: ""  # loaded from moebius-db secret

```

### 3. Install

```bash
helm install moebius ./deploy/helm/charts/moebius \
  -f my-values.yaml \
  -n moebius --create-namespace
```

The Helm chart automatically runs database migrations as a pre-install/pre-upgrade hook.

### 4. Bootstrap Admin

Run a one-off pod to create the initial admin:

```bash
kubectl run moebius-bootstrap --rm -it --restart=Never \
  --image=ghcr.io/eavalenzuela/moebius-api:latest \
  --env="DATABASE_URL=$(kubectl get secret moebius-db -o jsonpath='{.data.DATABASE_URL}' | base64 -d)" \
  -- create-admin --email admin@yourcompany.com
```

### Production Deployment

For production, layer the production values file:

```bash
helm install moebius ./deploy/helm/charts/moebius \
  -f deploy/helm/charts/moebius/values.production.yaml \
  -f my-values.yaml \
  -n moebius --create-namespace
```

Production defaults (`values.production.yaml`):

| Component | Replicas | Autoscaling   | CPU        | Memory       |
|-----------|----------|---------------|------------|--------------|
| API       | 5        | 5–40, 60% CPU | 500m–2000m | 512Mi–1Gi    |
| Scheduler | 2        | —             | 100m–500m  | 128Mi–256Mi  |

### Verify

```bash
kubectl get pods -n moebius
kubectl logs -n moebius deploy/moebius-api --tail=20
curl -s https://manage.yourcompany.com/health | jq .
```

---

## Agent Installation

### Linux

The agent is distributed as a tarball containing the binary, pkg-helper, install script, uninstall script, and systemd service unit.

```bash
# Download and extract (replace version/arch as needed)
tar xzf moebius-agent-linux-amd64-1.0.0.tar.gz
cd moebius-agent-linux-amd64-1.0.0

# Install
sudo ./install.sh \
  --enrollment-token <token> \
  --server-url https://manage.example.com \
  --ca-cert ./ca.crt           # optional: pin the server's CA
  --cdm-enabled                # optional: enable Customer Device Mode
```

The install script:
- Creates a `moebius-agent` system user and group
- Installs the binary to `/usr/local/bin/moebius-agent`
- Installs the setuid pkg-helper to `/usr/local/bin/moebius-pkg-helper`
- Writes config to `/etc/moebius-agent/config.toml`
- Installs and starts the systemd service
- Waits for enrollment and first check-in to complete

**Uninstall:**

```bash
sudo ./uninstall.sh
```

### Windows

The agent is distributed as a standalone `.exe` (MSI packaging is planned for WiX v4 builds on Windows CI runners).

```powershell
.\moebius-agent.exe install --enrollment-token <token> --server-url https://manage.example.com
```

### Agent File Locations (Linux)

| Path                              | Purpose                    |
|-----------------------------------|----------------------------|
| `/usr/local/bin/moebius-agent`    | Agent binary               |
| `/usr/local/bin/moebius-pkg-helper`| Setuid package helper     |
| `/etc/moebius-agent/`            | Configuration              |
| `/var/lib/moebius-agent/`        | Data directory             |
| `/var/lib/moebius-agent/drop/`   | File transfer drop dir     |
| `/var/log/moebius-agent/`        | Logs                       |
| `/run/moebius-agent/`            | Runtime (PID, sockets)     |

---

## Release Signing

All release artifacts are signed with an Ed25519 key. This section covers key generation and GitHub Actions configuration.

### Generate a Signing Keypair

```bash
go run ./tools/keygen -out-pub keys/release.pub -out-priv release.key
```

This produces:
- `keys/release.pub` — Base64-encoded public key. Commit this to the repository.
- `release.key` — Raw 64-byte private key. **Never commit this.** (It's in `.gitignore`.)

### Configure GitHub Actions

Base64-encode the private key and add it as a repository secret:

```bash
# Encode the private key
base64 < release.key | tr -d '\n'
```

Go to **Settings > Secrets and variables > Actions** in the GitHub repository and create a secret:

| Secret Name           | Value                                         |
|-----------------------|-----------------------------------------------|
| `RELEASE_SIGNING_KEY` | Base64-encoded contents of `release.key`      |

### Register the Public Key on the Server

After deploying the server, register the public key so agents can verify signed artifacts:

```bash
curl -X POST https://manage.example.com/v1/admin/signing-keys \
  -H "Authorization: Bearer sk_..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "GitHub Actions release key",
    "public_key": "<contents of keys/release.pub>",
    "key_type": "ed25519"
  }'
```

### Verify a Signature Manually

```bash
# The sign tool hashes (SHA-256) then signs. To verify:
# 1. Compute SHA-256 of the artifact
sha256sum moebius-agent-linux-amd64-1.0.0.tar.gz

# 2. The .sig file contains a base64-encoded Ed25519 signature of the hash
# Verification is handled by the agent and install scripts automatically
```

### Signing Workflow Summary

1. **Build time:** GitHub Actions runs `tools/sign` against each artifact using the `RELEASE_SIGNING_KEY` secret
2. **Container images:** Signed with cosign (keyless, OIDC-based) during the release workflow
3. **Agent verification:** The agent verifies Ed25519 signatures when downloading updates
4. **Install script:** Verifies checksum and signature before executing any installer code

---

## Release Pipeline (CI/CD)

### CI — Pull Requests and Pushes to `main`

Defined in `.github/workflows/ci.yml`. Runs on every PR and push to `main`:

| Job           | Description                                                    |
|---------------|----------------------------------------------------------------|
| `lint`        | golangci-lint                                                  |
| `vet`         | `go vet ./...`                                                 |
| `test`        | Unit tests with race detector and coverage                     |
| `integration` | Integration tests with PostgreSQL 16 service container         |
| `fmt`         | `gofmt` formatting check                                      |
| `build`       | Build matrix: api, scheduler, agent (linux/amd64, linux/arm64, windows/amd64) |

### Release — Tag Pushes

Defined in `.github/workflows/release.yml`. Triggered by pushing a tag matching `v*`:

**Agent build job** (matrix: linux/amd64, linux/arm64, windows/amd64):
1. Build agent binary and pkg-helper (Linux)
2. Package: tar.gz (Linux) or standalone .exe (Windows)
3. Compute SHA-256 checksums
4. Sign artifacts with Ed25519 release key (if `RELEASE_SIGNING_KEY` secret is set)
5. Upload artifacts

**Server image job:**
1. Build multi-arch Docker images (linux/amd64 + linux/arm64) for api, scheduler
2. Push to `ghcr.io/eavalenzuela/moebius-{api,scheduler}:{version}`
3. Tag `latest`
4. Sign images with cosign (keyless OIDC)

**Release job** (after agent + server jobs complete):
1. Download all artifacts
2. Create GitHub Release with auto-generated release notes
3. Attach all binaries, tarballs, checksums, and signatures

### Creating a Release

```bash
# Tag the commit
git tag v1.0.0
git push origin v1.0.0
```

The release workflow runs automatically. The GitHub Release page will contain all artifacts when complete.

---

## Database Migrations

Migrations live in `deploy/migrations/` and are embedded into the `moebius-api` binary.

### Running Migrations

**Local / Docker Compose:**

```bash
# Local binary
DATABASE_URL="postgres://..." ./dist/moebius-api migrate

# Docker Compose
docker compose run --rm api migrate
```

**Kubernetes:**

Migrations run automatically as a Helm pre-install/pre-upgrade hook. To run manually:

```bash
kubectl run moebius-migrate --rm -it --restart=Never \
  --image=ghcr.io/eavalenzuela/moebius-api:latest \
  --env="DATABASE_URL=$(kubectl get secret moebius-db -o jsonpath='{.data.DATABASE_URL}' | base64 -d)" \
  -- migrate
```

### Migration Files

| File                            | Description                                     |
|---------------------------------|-------------------------------------------------|
| `001_initial_schema.up.sql`     | Core tables: tenants, users, roles, devices, jobs, files |
| `001_initial_schema.down.sql`   | Rollback (not recommended in production)        |
| `002_device_logs.up.sql`        | Device log table with time-series indexes       |
| `003_rollouts.up.sql`           | Agent rollout tracking tables                   |

Migrations are forward-only in production. The `schema_migrations` table tracks which migrations have been applied.

---

## Database TLS

Moebius traffic to PostgreSQL carries credentials, tenant identifiers, audit-log writes, and signing-key material. Production deployments **must** require TLS on the database connection. The `DATABASE_URL` connection string controls this via the `sslmode` query parameter.

### Recommended modes

| Mode             | When to use                                                                 |
|------------------|------------------------------------------------------------------------------|
| `disable`        | Local development only; never in production.                                 |
| `require`        | Minimum acceptable for production. Encrypts the connection but does **not** verify the server certificate — vulnerable to MITM if an attacker can intercept TCP. |
| `verify-ca`      | Verifies the cert chains to a trusted CA. Defends against unknown servers.   |
| `verify-full`    | **Preferred for production.** Verifies chain *and* hostname match. Pair with `sslrootcert=` pointing at the CA bundle. |

### Self-Hosted (Docker Compose)

The bundled `postgres` service in `deploy/docker-compose.yml` listens only on the internal docker network — no host port is published — so traffic between the API/scheduler containers and the database never leaves the host. The default `sslmode=disable` is acceptable for evaluation in this configuration.

For production self-hosted installs, choose one of:

1. **External Postgres (recommended).** Point `DATABASE_URL` at an externally managed Postgres (RDS, Cloud SQL, on-prem cluster) configured for TLS. Override the URL via env var without editing compose:

   ```bash
   # In deploy/.env
   DATABASE_URL=postgres://moebius:PASSWORD@db.internal:5432/moebius?sslmode=verify-full&sslrootcert=/certs/db-ca.pem
   ```

   Mount the CA bundle into the api/scheduler containers (`./certs:/certs:ro` is already wired) and reference it via `sslrootcert`.

2. **TLS on the bundled Postgres.** Generate a server certificate, mount it into the postgres container, and start postgres with `ssl=on`. Then set `DATABASE_URL=...?sslmode=require` (or `verify-full` if you want hostname checking against the docker DNS name). This is more involved than option 1 and is rarely worth it for self-hosted installs.

### Kubernetes / Helm

The Helm chart loads `DATABASE_URL` from the `moebius-db` Secret. Create the secret with a TLS-enabled connection string:

```bash
kubectl create secret generic moebius-db \
  --from-literal=DATABASE_URL="postgres://moebius:PASSWORD@db-host:5432/moebius?sslmode=verify-full&sslrootcert=/etc/ssl/db-ca.pem"
```

If your Postgres CA needs to be mounted into the API/scheduler pods, add a `volumes` / `volumeMounts` override in your `my-values.yaml` and reference the in-pod path in `sslrootcert`. Cloud-managed databases (RDS, Cloud SQL, Azure DB for PostgreSQL) typically publish a CA bundle that should be baked into the API image or supplied via a ConfigMap.

### Verification

After deploying, confirm TLS is in effect:

```bash
# In a one-off pod / container with psql:
psql "$DATABASE_URL" -c "SHOW ssl;"
# Expected: ssl = on

# Or check from within Postgres:
psql -c "SELECT pid, ssl FROM pg_stat_ssl JOIN pg_stat_activity USING (pid) WHERE application_name LIKE '%moebius%';"
```

All Moebius connections should report `ssl = t`.

---

## Environment Variable Reference

### Shared (All Processes)

| Variable       | Required | Default | Description                          |
|----------------|----------|---------|--------------------------------------|
| `DATABASE_URL` | Yes      | —       | PostgreSQL connection string         |
| `LOG_LEVEL`    | No       | `info`  | `debug`, `info`, `warn`, `error`     |
| `LOG_FORMAT`   | No       | `json`  | `json` or `text`                     |
| `TENANT_MODE`  | No       | `multi` | `single` or `multi`                  |

### API Server

| Variable            | Required | Default       | Description                                |
|---------------------|----------|---------------|--------------------------------------------|
| `HTTP_PORT`         | No       | `8080`        | HTTP listen port                           |
| `TLS_MODE`          | No       | `passthrough` | `passthrough` (behind proxy) or `direct`   |
| `TLS_CERT_PATH`     | If direct| —             | TLS certificate path (direct mode only)    |
| `TLS_KEY_PATH`      | If direct| —             | TLS key path (direct mode only)            |
| `CA_CERT_PATH`      | Yes      | —             | Intermediate CA certificate for agent mTLS |
| `CA_KEY_PATH`       | Yes      | —             | Intermediate CA private key                |
| `TRUSTED_PROXY_CIDRS`| No      | Private nets  | CIDRs trusted to forward `X-Client-Cert`  |
| `STORAGE_BACKEND`   | No       | `local`       | `local` or `s3`                            |
| `STORAGE_PATH`      | If local | `/tmp/moebius-storage` | Local file storage directory     |
| `S3_ENDPOINT`       | If s3    | —             | S3-compatible endpoint URL                 |
| `S3_BUCKET`         | If s3    | —             | S3 bucket name                             |
| `S3_REGION`         | If s3    | —             | S3 region                                  |
| `S3_ACCESS_KEY_ID`  | If s3    | —             | S3 access key                              |
| `S3_SECRET_ACCESS_KEY`| If s3  | —             | S3 secret key                              |
| `OIDC_ISSUER_URL`   | No       | —             | OIDC provider issuer URL                   |
| `OIDC_CLIENT_ID`    | No       | —             | OIDC client ID                             |
| `OIDC_CLIENT_SECRET`| No       | —             | OIDC client secret                         |

### Scheduler

| Variable                            | Required | Default | Description                                                          |
|-------------------------------------|----------|---------|----------------------------------------------------------------------|
| `SCHEDULER_TICK_SECONDS`            | No       | `30`    | Cron/reaper evaluation interval                                      |
| `REAPER_DISPATCHED_TIMEOUT_SECONDS` | No       | `300`   | Dispatched jobs older than this are requeued                         |
| `REAPER_INFLIGHT_TIMEOUT_SECONDS`   | No       | `3600`  | Acknowledged/running jobs older than this are marked `timed_out`     |
| `SMTP_HOST`                         | No       | —       | SMTP server for alert emails                                         |
| `SMTP_PORT`                         | No       | `587`   | SMTP port                                                            |
| `SMTP_USERNAME`         | No       | —                 | SMTP auth username              |
| `SMTP_PASSWORD`         | No       | —                 | SMTP auth password              |
| `SMTP_FROM`             | No       | `moebius@localhost`| Sender address for alerts      |

---

## Upgrading

### Docker Compose

```bash
cd deploy

# Pull new images
docker compose pull

# Run migrations (safe to re-run; only applies new ones)
docker compose run --rm api migrate

# Restart with new images
docker compose up -d
```

### Kubernetes / Helm

```bash
helm upgrade moebius ./deploy/helm/charts/moebius \
  -f my-values.yaml \
  -n moebius
```

The Helm pre-upgrade hook runs migrations automatically before rolling out new pods.

### Agent

Agents can be updated via:
1. **Auto-update job:** Create an `agent_update` job targeting devices. The agent downloads the new version, verifies the signature, stages it, and restarts.
2. **Manual upgrade:** On the endpoint, run `sudo ./install.sh --upgrade --binary ./moebius-agent-new`

---

## Troubleshooting

### Health Checks

```bash
# Liveness — returns 200 if process is running
curl http://localhost:8080/health

# Readiness — returns 200 if database is reachable
curl http://localhost:8080/health/ready
```

### Logs

```bash
# Docker Compose
docker compose logs -f api
docker compose logs -f scheduler

# Kubernetes
kubectl logs -f deploy/moebius-api -n moebius
kubectl logs -f deploy/moebius-scheduler -n moebius

# Agent (Linux)
journalctl -u moebius-agent -f
# or
cat /var/log/moebius-agent/agent.log
```

### Common Issues

**API won't start: "missing required environment variables: CA_CERT_PATH, CA_KEY_PATH"**
Run `generate-ca` first. See steps above for your deployment method.

**Agent enrollment fails**
- Verify the enrollment token is valid and hasn't expired
- Check that the agent can reach the server URL
- If using a custom CA, pass `--ca-cert` to the install script

**Migrations fail**
- Verify `DATABASE_URL` is correct and the database is reachable
- Check PostgreSQL logs for connection/permission errors
- Migrations are idempotent — safe to retry

**Container images not found**
- Ensure you're authenticated to GHCR: `docker login ghcr.io`
- Check the image tag matches the release version
