-- 001_initial_schema.up.sql
-- Frozen from deploy/schema.sql at Phase 6.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─── Tenants ────────────────────────────────────────────

CREATE TABLE tenants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    config     JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Users & Auth ───────────────────────────────────────

CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id),
    name        TEXT NOT NULL,
    permissions JSONB NOT NULL,
    is_custom   BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id),
    email       TEXT NOT NULL,
    role_id     UUID REFERENCES roles(id),
    sso_subject TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id),
    user_id      UUID REFERENCES users(id),
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL,
    role_id      UUID REFERENCES roles(id),
    scope        JSONB,
    is_admin     BOOLEAN NOT NULL DEFAULT FALSE,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Devices ────────────────────────────────────────────

CREATE TABLE devices (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hostname               TEXT NOT NULL,
    tenant_id              UUID NOT NULL REFERENCES tenants(id),
    os                     TEXT NOT NULL,
    os_version             TEXT NOT NULL,
    arch                   TEXT NOT NULL,
    agent_version          TEXT NOT NULL,
    status                 TEXT NOT NULL DEFAULT 'unknown',
    last_seen_at           TIMESTAMPTZ,
    registered_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    cdm_enabled            BOOLEAN NOT NULL DEFAULT FALSE,
    cdm_session_active     BOOLEAN NOT NULL DEFAULT FALSE,
    cdm_session_expires_at TIMESTAMPTZ,
    sequence_last          BIGINT NOT NULL DEFAULT 0
);

-- ─── Grouping ───────────────────────────────────────────

CREATE TABLE groups (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name      TEXT NOT NULL
);

CREATE TABLE device_groups (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    group_id  UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (device_id, group_id)
);

CREATE TABLE tags (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name      TEXT NOT NULL
);

CREATE TABLE device_tags (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    tag_id    UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (device_id, tag_id)
);

CREATE TABLE sites (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name      TEXT NOT NULL,
    location  TEXT
);

CREATE TABLE device_sites (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    site_id   UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    PRIMARY KEY (device_id, site_id)
);

-- ─── Inventory ──────────────────────────────────────────

CREATE TABLE inventory_hardware (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id          UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    collected_at       TIMESTAMPTZ NOT NULL,
    cpu                JSONB,
    ram_mb             BIGINT,
    disks              JSONB,
    network_interfaces JSONB
);

CREATE TABLE inventory_packages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    version      TEXT NOT NULL,
    manager      TEXT NOT NULL,
    installed_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ NOT NULL
);

-- ─── Jobs ───────────────────────────────────────────────

CREATE TABLE jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    device_id       UUID NOT NULL REFERENCES devices(id),
    parent_job_id   UUID REFERENCES jobs(id),
    type            TEXT NOT NULL,
    status          TEXT NOT NULL,
    payload         JSONB NOT NULL,
    retry_policy    JSONB,
    retry_count     INT NOT NULL DEFAULT 0,
    max_retries     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at   TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ
);

CREATE TABLE job_results (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id       UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    exit_code    INT,
    stdout       TEXT,
    stderr       TEXT,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

-- ─── Scheduled Jobs ─────────────────────────────────────

CREATE TABLE scheduled_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id),
    name         TEXT NOT NULL,
    job_type     TEXT NOT NULL,
    payload      JSONB NOT NULL,
    target       JSONB NOT NULL,
    cron_expr    TEXT NOT NULL,
    retry_policy JSONB,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at  TIMESTAMPTZ,
    next_run_at  TIMESTAMPTZ
);

-- ─── Audit Log ──────────────────────────────────────────

CREATE TABLE audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    actor_id      UUID NOT NULL,
    actor_type    TEXT NOT NULL,
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   UUID,
    metadata      JSONB,
    ip_address    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Alert Rules ────────────────────────────────────────

CREATE TABLE alert_rules (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name      TEXT NOT NULL,
    condition JSONB NOT NULL,
    channels  JSONB NOT NULL,
    enabled   BOOLEAN NOT NULL DEFAULT TRUE
);

-- ─── Agent Certificates ─────────────────────────────────

CREATE TABLE agent_certificates (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    serial_number     TEXT NOT NULL UNIQUE,
    fingerprint       TEXT NOT NULL UNIQUE,
    issued_at         TIMESTAMPTZ NOT NULL,
    expires_at        TIMESTAMPTZ NOT NULL,
    revoked_at        TIMESTAMPTZ,
    revocation_reason TEXT
);

-- ─── Enrollment Tokens ──────────────────────────────────

CREATE TABLE enrollment_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id),
    token_hash TEXT NOT NULL UNIQUE,
    created_by UUID NOT NULL REFERENCES users(id),
    scope      JSONB,
    used_at    TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Signing Keys ───────────────────────────────────────

CREATE TABLE signing_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id),
    name        TEXT NOT NULL,
    algorithm   TEXT NOT NULL DEFAULT 'ed25519',
    public_key  TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Files ──────────────────────────────────────────────

CREATE TABLE files (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id),
    filename           TEXT NOT NULL,
    size_bytes         BIGINT NOT NULL,
    sha256             TEXT NOT NULL,
    signature          TEXT,
    signature_key_id   UUID REFERENCES signing_keys(id),
    signature_verified BOOLEAN NOT NULL DEFAULT FALSE,
    mime_type          TEXT,
    storage_backend    TEXT NOT NULL,
    storage_path       TEXT NOT NULL,
    created_by         UUID REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE file_uploads (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id          UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    tenant_id        UUID NOT NULL REFERENCES tenants(id),
    chunk_size_bytes INT NOT NULL,
    total_chunks     INT NOT NULL,
    uploaded_chunks  INT[] NOT NULL DEFAULT '{}',
    completed_at     TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─── Agent Versions ─────────────────────────────────────

CREATE TABLE agent_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version     TEXT NOT NULL UNIQUE,
    channel     TEXT NOT NULL,
    changelog   TEXT,
    yanked      BOOLEAN NOT NULL DEFAULT FALSE,
    yank_reason TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agent_version_binaries (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_version_id UUID NOT NULL REFERENCES agent_versions(id) ON DELETE CASCADE,
    os               TEXT NOT NULL,
    arch             TEXT NOT NULL,
    file_id          UUID NOT NULL REFERENCES files(id),
    sha256           TEXT NOT NULL,
    signature        TEXT NOT NULL,
    signature_key_id UUID NOT NULL REFERENCES signing_keys(id),
    UNIQUE (agent_version_id, os, arch)
);

CREATE TABLE agent_update_policies (
    id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                      UUID NOT NULL REFERENCES tenants(id),
    group_id                       UUID REFERENCES groups(id),
    enabled                        BOOLEAN NOT NULL DEFAULT TRUE,
    channel                        TEXT NOT NULL DEFAULT 'stable',
    schedule                       TEXT,
    rollout_strategy               TEXT NOT NULL DEFAULT 'gradual',
    rollout_batch_percent          INT NOT NULL DEFAULT 10,
    rollout_batch_interval_minutes INT NOT NULL DEFAULT 60,
    UNIQUE (tenant_id, group_id)
);

-- ─── Installers ─────────────────────────────────────────

CREATE TABLE installers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version          TEXT NOT NULL,
    channel          TEXT NOT NULL,
    os               TEXT NOT NULL,
    arch             TEXT NOT NULL,
    file_id          UUID NOT NULL REFERENCES files(id),
    sha256           TEXT NOT NULL,
    signature        TEXT NOT NULL,
    signature_key_id UUID NOT NULL REFERENCES signing_keys(id),
    released_at      TIMESTAMPTZ NOT NULL,
    yanked           BOOLEAN NOT NULL DEFAULT FALSE,
    yank_reason      TEXT,
    UNIQUE (version, os, arch)
);

-- ─── Indexes ────────────────────────────────────────────

CREATE INDEX idx_devices_tenant     ON devices(tenant_id);
CREATE INDEX idx_jobs_tenant        ON jobs(tenant_id);
CREATE INDEX idx_jobs_device        ON jobs(device_id);
CREATE INDEX idx_jobs_status        ON jobs(tenant_id, status);
CREATE INDEX idx_audit_log_tenant   ON audit_log(tenant_id, created_at DESC);
CREATE INDEX idx_users_tenant       ON users(tenant_id);
CREATE INDEX idx_api_keys_tenant    ON api_keys(tenant_id);
CREATE INDEX idx_groups_tenant      ON groups(tenant_id);
CREATE INDEX idx_tags_tenant        ON tags(tenant_id);
CREATE INDEX idx_sites_tenant       ON sites(tenant_id);
CREATE INDEX idx_files_tenant       ON files(tenant_id);
CREATE INDEX idx_agent_certs_device ON agent_certificates(device_id);
CREATE INDEX idx_enrollment_tokens_hash ON enrollment_tokens(token_hash);
CREATE INDEX idx_inventory_hw_device  ON inventory_hardware(device_id);
CREATE INDEX idx_inventory_pkg_device ON inventory_packages(device_id);
CREATE INDEX idx_scheduled_jobs_next ON scheduled_jobs(next_run_at) WHERE enabled = TRUE;
