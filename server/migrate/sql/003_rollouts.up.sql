-- Gradual rollout tracking for agent version deployments

CREATE TABLE agent_rollouts (
    id                       TEXT PRIMARY KEY,
    agent_version_id         TEXT NOT NULL REFERENCES agent_versions(id) ON DELETE CASCADE,
    tenant_id                TEXT NOT NULL REFERENCES tenants(id),
    status                   TEXT NOT NULL DEFAULT 'in_progress',
    strategy                 TEXT NOT NULL DEFAULT 'gradual',
    batch_percent            INT NOT NULL DEFAULT 10,
    batch_interval_minutes   INT NOT NULL DEFAULT 60,
    current_batch            INT NOT NULL DEFAULT 0,
    total_devices            INT NOT NULL DEFAULT 0,
    updated_devices          INT NOT NULL DEFAULT 0,
    rolled_back_devices      INT NOT NULL DEFAULT 0,
    seed                     BIGINT NOT NULL DEFAULT 0,
    last_batch_at            TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_version_id, tenant_id)
);

CREATE INDEX idx_agent_rollouts_tenant ON agent_rollouts(tenant_id);
CREATE INDEX idx_agent_rollouts_status ON agent_rollouts(status) WHERE status = 'in_progress';

-- Track which devices have been updated per rollout
CREATE TABLE agent_rollout_devices (
    rollout_id   TEXT NOT NULL REFERENCES agent_rollouts(id) ON DELETE CASCADE,
    device_id    TEXT NOT NULL REFERENCES devices(id),
    batch        INT NOT NULL,
    job_id       TEXT REFERENCES jobs(id),
    status       TEXT NOT NULL DEFAULT 'pending',
    PRIMARY KEY (rollout_id, device_id)
);
