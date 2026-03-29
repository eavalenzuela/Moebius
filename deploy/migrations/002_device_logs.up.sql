-- 002_device_logs.up.sql
-- Phase 10: Log Shipping — stores agent log entries shipped via POST /v1/agents/logs.

CREATE TABLE device_logs (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    timestamp  TIMESTAMPTZ NOT NULL,
    level      TEXT NOT NULL,
    message    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_device_logs_device_ts ON device_logs(device_id, timestamp DESC);
CREATE INDEX idx_device_logs_tenant_ts ON device_logs(tenant_id, timestamp DESC);
