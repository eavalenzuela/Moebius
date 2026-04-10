# Audit Log Retention

This document covers operational guidance for managing the `audit_log` table
in a long-running Moebius deployment. It answers three questions operators
hit after the first few months of production: how big will this table get,
how do I prune it without violating the append-only invariant, and how do I
monitor for lost audit entries.

Security design decisions for audit logging live in `SECURITY.md` and the
Audit Log Integrity section of `SEC_VALIDATION.md`; this file is strictly
the ops runbook.

---

## Schema and Invariants

The `audit_log` table is created in migration `001_initial_schema.up.sql`
and hardened by migration `004_audit_log_immutable.up.sql`:

- Two PostgreSQL rules — `audit_log_no_update` and `audit_log_no_delete` —
  turn `UPDATE` and `DELETE` into silent no-ops. Even a compromised API
  server or a SQL-injection bug cannot modify or remove audit rows through
  normal DML.
- A `BEFORE TRUNCATE` trigger raises an exception, so `TRUNCATE audit_log`
  fails loudly rather than silently succeeding.

The upshot for retention: **you cannot simply `DELETE FROM audit_log`**.
Any pruning strategy has to either drop the rules temporarily under a
maintenance window or use table partitioning so old data can be removed
via `DROP TABLE` (a DDL operation that bypasses the row-level rules).

---

## Growth Expectations

Rough upper bound per row: ~1 KB including the JSONB metadata payload.
Rates depend heavily on fleet size and operator activity; as a starting
point:

| Activity profile                                   | Rows/day |
|-----------------------------------------------------|---------:|
| 100 agents, light operator use                      |    5,000 |
| 1,000 agents, normal operator use                   |   50,000 |
| 10,000 agents, heavy automation and job creation    |  500,000 |

At the 10,000-agent tier that is ~500 MB/day or ~180 GB/year. Without a
retention policy the table will grow linearly forever. Query performance
stays acceptable for the lifetime of any reasonable single-table
deployment, but storage and backup size do not.

---

## Strategy 1 — Periodic Pruning (Simple)

Appropriate for: small-to-medium deployments (<1000 agents), deployments
with a fixed compliance horizon (e.g. "keep 18 months"), operators who
prefer a single scheduled SQL job over schema changes.

Pruning requires temporarily dropping the `audit_log_no_delete` rule,
running the `DELETE`, and recreating the rule. Run this during a
maintenance window under a transaction so a failure mid-way leaves the
rule reinstated:

```sql
BEGIN;

-- Lift the delete rule so the prune can proceed.
DROP RULE audit_log_no_delete ON audit_log;

-- Prune rows older than the retention horizon. Adjust interval to taste.
DELETE FROM audit_log
 WHERE created_at < now() - INTERVAL '18 months';

-- Reinstate the append-only invariant before committing.
CREATE RULE audit_log_no_delete AS ON DELETE TO audit_log DO INSTEAD NOTHING;

COMMIT;
```

Notes:

- The `DROP RULE` / `CREATE RULE` requires the role that owns the table
  (normally the service user or the migration role). Superuser is not
  required.
- `DELETE` of a large batch will take a long time and generate a lot of
  WAL. For initial cleanup of an already-huge table, break it into batches
  of `created_at < ... LIMIT 100000` loops, each in its own transaction,
  so the delete rule is only lifted for short windows.
- After a bulk prune, run `VACUUM (ANALYZE) audit_log` to reclaim space
  and refresh planner statistics.
- If you archive before pruning (recommended for compliance), run a
  `COPY ... TO PROGRAM 'gzip > audit-YYYYMM.csv.gz'` inside the same
  transaction before the `DELETE`.

**Schedule:** run via cron (or a scheduled Kubernetes `Job`) on a
monthly cadence at low traffic. The scheduler binary intentionally does
not own this job — retention is an operator policy, not a product
behavior, and should not silently change across Moebius upgrades.

---

## Strategy 2 — RANGE Partitioning (Recommended at Scale)

Appropriate for: deployments with >1000 agents, aggressive retention
windows, deployments that need to archive partitions to cold storage.

Convert `audit_log` to a range-partitioned table on `created_at`, one
partition per month. Old data is removed by `DROP TABLE` on whole
partitions, which bypasses the row-level `DO INSTEAD NOTHING` rules
because DDL is not DML. Each partition still inherits the append-only
rules, so rows cannot be modified in place.

This is a schema change and must be done via a migration when the
deployment can tolerate an exclusive lock on `audit_log` for the
duration of the swap. The sketch below is what the migration would do;
the shipped Moebius schema has not adopted this by default because it
imposes operational complexity on small deployments that do not need it.

```sql
-- 1. Rename the existing table out of the way.
ALTER TABLE audit_log RENAME TO audit_log_legacy;

-- 2. Create the partitioned parent. Same columns, same PK, plus
--    created_at is promoted into the primary key so each partition
--    can enforce uniqueness locally.
CREATE TABLE audit_log (
    id            TEXT NOT NULL,
    tenant_id     TEXT NOT NULL,
    actor_id      TEXT NOT NULL,
    actor_type    TEXT NOT NULL,
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    metadata      JSONB,
    ip_address    TEXT,
    created_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- 3. Create a partition per month going forward. Automate this — a
--    missing future partition causes INSERTs to fail. pg_partman or a
--    scheduled job that runs `CREATE TABLE IF NOT EXISTS audit_log_YYYY_MM
--    PARTITION OF audit_log FOR VALUES FROM ... TO ...` is standard.
CREATE TABLE audit_log_2026_04 PARTITION OF audit_log
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
-- (repeat per month)

-- 4. Re-apply the append-only rules at the parent level. Child partitions
--    inherit them automatically for DML.
CREATE RULE audit_log_no_update AS ON UPDATE TO audit_log DO INSTEAD NOTHING;
CREATE RULE audit_log_no_delete AS ON DELETE TO audit_log DO INSTEAD NOTHING;

-- 5. Re-apply the TRUNCATE trigger. Triggers do NOT cascade to children
--    automatically — you need one per partition, or a shared function
--    referenced by triggers on each child.
CREATE TRIGGER no_truncate_audit_log
BEFORE TRUNCATE ON audit_log
FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_log_truncate();

-- 6. Backfill from the legacy table, then drop it.
INSERT INTO audit_log SELECT * FROM audit_log_legacy;
DROP TABLE audit_log_legacy;
```

Pruning then becomes a single `DROP TABLE audit_log_2024_01;` per month,
which is fast, reclaims storage immediately, and does not require lifting
the append-only rules.

**Partition management:** use
[`pg_partman`](https://github.com/pgpartman/pg_partman) or a small cron
job in the deployment's own infrastructure. Moebius does not ship this
automation.

---

## Archival Before Deletion

For compliance-sensitive deployments, prune is almost always "archive
first, then delete". Two patterns work well:

1. **Per-partition archival** (pairs with Strategy 2). Before dropping a
   partition, `COPY audit_log_2024_01 TO PROGRAM 'gzip > /archive/audit-2024-01.csv.gz'`
   writes the whole partition out. Copy the gzip to object storage
   (S3, GCS) and then `DROP TABLE`. Partition granularity keeps the
   archival job idempotent.
2. **Date-range archival** (pairs with Strategy 1). Inside the same
   transaction as the `DELETE`, run a `COPY (SELECT ... WHERE created_at < ...) TO PROGRAM ...`
   before the delete. If the copy fails the transaction rolls back and
   the rows are preserved.

The Moebius schema does not include archival logic because the right
destination is deployment-specific: S3 for SaaS, an SMB share for
on-prem, a SIEM forwarder for compliance-heavy customers.

---

## Monitoring Lost Audit Writes

The server exposes a Prometheus counter **`audit_write_failures_total`**
(see `server/metrics/metrics.go`). A non-zero value means an audit entry
was lost — the handler returned success to the caller but the `INSERT`
into `audit_log` failed. Causes in order of likelihood: database
outage, connection pool exhaustion, disk-full on the audit tablespace.

Alert on **any** non-zero delta over a short window — audit writes
failing silently is exactly what this metric exists to surface:

```yaml
# Example Prometheus alert
- alert: MoebiusAuditWriteFailure
  expr: increase(audit_write_failures_total[5m]) > 0
  for: 0m
  labels:
    severity: critical
  annotations:
    summary: "Moebius audit log write failed"
    description: |
      At least one audit entry was lost in the last 5 minutes. Check the
      database health and the structured log for 'audit log write failed'
      entries with the affected action and tenant_id.
```

The structured log line that accompanies each failure is
`audit log write failed` at `level=error`, with `action`, `actor_id`,
`resource_type`, `resource_id`, `tenant_id`, and `error` fields so the
specific lost entry can be reconstructed from log output if needed.

Compliance-sensitive deployments that cannot tolerate any lost audit
entries — even under DB outage — need a write-ahead audit pattern
(persist the audit entry to a local disk spool before acknowledging the
operation, replay on DB recovery). That is **not implemented** and is
tracked as a separate hardening item in `SEC_VALIDATION_FINDINGS.md §
Outstanding Remediation Work`.

---

## Indexing for Operator Queries

The default migration adds no secondary indexes to `audit_log`. The most
common operator query is
`WHERE tenant_id = $1 AND created_at > $2 ORDER BY created_at DESC`
for the in-app audit viewer. At the row volumes the table will reach
without pruning, that query will become slow enough to matter.

If pruning alone is not bringing query latency into an acceptable range,
add:

```sql
CREATE INDEX CONCURRENTLY idx_audit_log_tenant_created
    ON audit_log (tenant_id, created_at DESC);
```

`CONCURRENTLY` avoids blocking writes. On a large unpartitioned table
the index build will take a long time — schedule it during a low
traffic window. In a partitioned deployment, apply the index to the
parent table and PostgreSQL will create it on each partition.
