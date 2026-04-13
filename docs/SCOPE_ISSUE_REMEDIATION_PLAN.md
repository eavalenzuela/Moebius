# API Key Scope Enforcement — Remediation Plan

**Finding source:** SEC_VALIDATION.md, invariant I3 notes (separate finding)
**Severity:** High — advertised security guarantee (SECURITY.md, REST_API_SPEC.md) is not enforced
**Date opened:** 2026-04-05

---

## Problem Statement

API keys support a `scope` field (`models.APIScope`) that restricts the key to specific groups, tags, sites, or devices:

```go
type APIScope struct {
    GroupIDs  []string `json:"group_ids,omitempty"`
    TagIDs    []string `json:"tag_ids,omitempty"`
    SiteIDs   []string `json:"site_ids,omitempty"`
    DeviceIDs []string `json:"device_ids,omitempty"`
}
```

The auth middleware (`server/auth/apikey.go:130-155`) correctly parses the scope from the database and places it in context via `ContextKeyScope`. The helper `ScopeFromContext()` is exported and tested.

**But no handler reads it.** A scoped API key passes `rbac.Require(PermJobsCreate)` and can then create jobs targeting *any* device in the tenant. The scope is silently dropped.

This affects every resource endpoint that operates on devices or device-associated data. SECURITY.md line 72 advertises:

> "Scoped keys can only access resources within their scope, providing fine-grained access control for automation and third-party integrations."

This guarantee is not actually enforced.

---

## Design Decisions

### Where scope is enforced

Scope enforcement belongs in the **API handler layer** (not middleware, not store). Reasons:

1. **Scope semantics are resource-specific.** A scoped key listing devices should filter results. A scoped key creating a job should intersect target devices with scope. A scoped key revoking a device should check the device is in scope. These are different operations — middleware cannot generalize them.

2. **RBAC middleware stays simple.** `rbac.Require` checks permissions (role-level). Scope is a further restriction *within* an allowed permission, not a separate permission. Keeping them separate avoids coupling.

3. **Store layer stays scope-unaware.** The store already takes explicit filter parameters. Handlers will compute the allowed device set from the scope, then pass it down. This avoids pushing authz logic into the data layer.

### Scope model: intersection, not union

A scope with `group_ids: ["A"]` and `device_ids: ["dev_X"]` means the key can access devices in group A **and** device dev_X (union of the listed IDs). All scope fields are unioned to produce the set of allowed device IDs.

A `nil` scope (unscoped key) has no restriction — equivalent to tenant-wide access.

### Admin bypass

API keys with `is_admin=true` bypass scope checks (same as RBAC). This is intentional and documented.

---

## Remediation Tasks

### Task 1 — Scope resolution helper

**File:** `server/auth/scope.go` (new)

Create a helper that resolves an `APIScope` to a set of allowed device IDs:

```go
func ResolveScope(ctx context.Context, pool *pgxpool.Pool, tenantID string, scope *APIScope) (allowedIDs map[string]struct{}, err error)
```

- If `scope == nil`, return `nil` (meaning "unrestricted").
- Union all `DeviceIDs` directly.
- Expand `GroupIDs` → device IDs via `device_groups` join.
- Expand `TagIDs` → device IDs via `device_tags` join.
- Expand `SiteIDs` → device IDs via `device_sites` join.
- Return deduplicated set.

Also add a convenience:

```go
func DeviceInScope(allowed map[string]struct{}, deviceID string) bool
```

Returns `true` if `allowed` is nil (unscoped) or if `deviceID` is in the set.

**Unit tests:** `server/auth/scope_test.go` — test nil scope, device-only scope, group expansion, union of multiple fields.

---

### Task 2 — Scope-aware job creation

**File:** `server/api/jobs.go` — `Create` handler

After resolving targets (`resolveTargets`), intersect the result with the scope:

1. Call `auth.ScopeFromContext(ctx)`.
2. If admin (`auth.IsAdminFromContext`), skip scope check.
3. If scope is non-nil, call `auth.ResolveScope(...)`, then filter `deviceIDs` to only those in the allowed set.
4. If the intersection is empty, return `403 Forbidden` with code `scope_violation` and a message indicating the target devices are outside the key's scope.

**Scope-check for cancel/retry:** `Cancel` and `Retry` operate on a single job by ID. After loading the job, check `DeviceInScope(allowed, job.DeviceID)`. Return 403 if out of scope.

---

### Task 3 — Scope-aware device endpoints

**File:** `server/api/devices.go`

**List:** After loading scope, if non-nil, add a `WHERE d.id IN (...)` clause (or pass scope device IDs as a filter to the store). Implementation options:

- Option A: Expand scope to device IDs in the handler, add to `DeviceFilters` as a new `ScopeDeviceIDs []string` field.
- Option B: Post-filter in the handler (simpler but wastes DB reads on large tenants).

**Prefer Option A** — add `ScopeDeviceIDs` to `store.DeviceFilters`. When non-empty, the store appends `AND d.id = ANY($N)`.

**Get:** After loading the device, check `DeviceInScope(allowed, deviceID)`. Return 404 (not 403 — don't leak existence) if out of scope.

**Update:** Same as Get — check scope before allowing the update.

**Revoke:** Same as Get — check scope before allowing the revoke.

---

### Task 4 — Scope-aware device inventory

**File:** `server/api/inventory.go` — `GetDeviceInventory`

Extracts `device_id` from the URL. Check `DeviceInScope` before returning inventory data. Return 404 if out of scope.

---

### Task 5 — Scope-aware job listing

**File:** `server/api/jobs.go` — `List` handler

Jobs are associated with devices. When scope is active, restrict the job listing to jobs whose `device_id` is in the allowed set.

Add `ScopeDeviceIDs` filter to the job listing query (similar to the existing `device_id` filter, but using `IN` instead of `=`).

**Get (single job):** After loading the job, check `DeviceInScope(allowed, job.DeviceID)`. Return 404 if out of scope.

---

### Task 6 — Scope-aware group/tag/site listing

**Files:** `server/api/groups.go`, `server/api/tags.go`, `server/api/sites.go`

Scope restricts which groups/tags/sites are visible:

- **Groups:** If scope has `GroupIDs`, `List` returns only those groups. `Get`, `Update`, `Delete`, `ListDevices`, `AddDevices`, `RemoveDevice` — check group is in scope's `GroupIDs`.
- **Tags:** Same pattern with `TagIDs`.
- **Sites:** Same pattern with `SiteIDs`.

If scope has no `GroupIDs` but has `DeviceIDs`, the key can see groups that contain those devices (derived access). For simplicity in the first pass, if a scope field is empty, the key has unrestricted access to that resource type — only non-empty scope fields restrict. Document this decision.

---

### Task 7 — Scope-aware scheduled jobs and alert rules

**Files:** `server/api/scheduled_jobs.go`, `server/api/alert_rules.go`

**Scheduled jobs:** Creating a scheduled job resolves targets. Apply the same scope intersection as Task 2 (at creation time). Listing: filter to scheduled jobs whose targets overlap with scope. Get/Update/Delete: check target overlap.

**Alert rules:** Alert rule `scope` targets are checked at creation time to ensure they fall within the key's scope. Listing: filter to rules whose scope overlaps. Get/Update/Delete: check scope overlap.

---

### Task 8 — Scope-aware file operations

**File:** `server/api/files.go`

Files are tenant-wide, not device-scoped. **No scope enforcement needed** on file CRUD — files are shared resources within a tenant. Document this decision.

Exception: if a file is linked to a specific device (via a file_transfer job), the job creation is already scope-gated (Task 2). The file itself remains accessible to any key with `files:read`.

---

### Task 9 — Scope-aware enrollment tokens

**File:** `server/api/enrollment_tokens.go`

Enrollment tokens have their own `scope` field (used to assign devices to groups on enrollment). A scoped API key creating an enrollment token should only be able to set a token scope that is a subset of its own scope. Validate this at creation time.

---

### Task 10 — Admin bypass audit

**Files:** all handlers touched in Tasks 2-9

Verify that every scope check is skipped when `auth.IsAdminFromContext(ctx)` is true. This is consistent with the RBAC bypass already in place. Add a code comment at each check site referencing this policy.

---

### Task 11 — Integration tests

**File:** `tests/integration/scope_test.go` (new)

Tests using the existing integration harness:

| # | Test | Validates |
|---|------|-----------|
| 1 | `TestScope_KeyScopedToGroup_CanOnlySeeGroupDevices` | Device list filtering |
| 2 | `TestScope_KeyScopedToGroup_CannotGetDeviceOutsideScope` | Device get returns 404 |
| 3 | `TestScope_KeyScopedToGroup_CannotRevokeDeviceOutsideScope` | Device revoke returns 404 |
| 4 | `TestScope_KeyScopedToGroup_JobCreateIntersectsScope` | Job creation filters targets |
| 5 | `TestScope_KeyScopedToGroup_JobCreateAllOutOfScope` | Job creation returns 403 |
| 6 | `TestScope_KeyScopedToGroup_CannotCancelJobOutsideScope` | Job cancel returns 403 |
| 7 | `TestScope_KeyScopedToGroup_JobListFiltered` | Job list returns only in-scope jobs |
| 8 | `TestScope_UnscopedKey_SeesAllDevices` | Nil scope = no restriction |
| 9 | `TestScope_AdminKey_BypassesScope` | `is_admin=true` ignores scope |
| 10 | `TestScope_KeyScopedToDeviceIDs_DirectScope` | Device ID scope (no group indirection) |
| 11 | `TestScope_KeyScopedToMultipleGroups_Union` | Multiple group IDs unioned |

These tests must create scoped API keys via the harness, which requires extending `createAPIKeyWithPerms` to accept an optional `*models.APIScope` parameter (or adding a `createScopedAPIKey` helper).

---

### Task 12 — Documentation updates

- **SECURITY.md** line 72: Add a note that scope is enforced per-request at the handler level, with admin bypass.
- **REST_API_SPEC.md**: Document scope behavior in the API Keys section (which endpoints are scope-restricted, which are not).
- **SEC_VALIDATION.md**: Close the I3 scope finding with references to the new code and test coverage.

---

## Implementation Status

| Task | Status | Notes |
|------|--------|-------|
| Task 1 — Scope resolution helper | ✅ Done | `server/auth/scope.go`: `ResolveScope`, `DeviceInScope`, `FilterDeviceIDs`, `ScopeHasField`, `IDInScopeField`, `TargetOverlapsScope`, `ScopeIsSubset`. Unit tests in `scope_test.go`. |
| Task 2 — Jobs: create, cancel, retry | ✅ Done | `jobs.go`: Create intersects resolved targets with scope. Cancel/Retry load device_id, check scope. |
| Task 3 — Devices: list, get, update, revoke | ✅ Done | `devices.go`: List uses `ScopeDeviceIDs` filter. Get/Update/Revoke check `DeviceInScope`. Returns 404 (not 403) for out-of-scope. |
| Task 4 — Inventory | ✅ Done | `inventory.go`: `GetDeviceInventory` checks `DeviceInScope`. |
| Task 5 — Jobs: list, get | ✅ Done | `jobs.go`: List adds `device_id = ANY(...)` filter. Get checks `DeviceInScope`. |
| Task 6 — Groups, tags, sites | ✅ Done | List post-filters by scope field. Get/Update/Delete/membership check `IDInScopeField`. Create blocked for scoped keys. Tag add/remove to device checks `DeviceInScope`. |
| Task 7 — Scheduled jobs | ✅ Done | `scheduled_jobs.go`: Create validates `TargetOverlapsScope`. Alert rules: no device-scoped fields (condition is opaque JSON), no scope enforcement needed. |
| Task 8 — Files | ✅ N/A | Files are tenant-wide, not device-scoped. No scope enforcement needed. Job creation (Task 2) gates file-transfer jobs. |
| Task 9 — Enrollment tokens | ✅ Done | `enrollment_tokens.go`: Create validates `ScopeIsSubset(keyScope, tokenScope)`. |
| Task 10 — Admin bypass | ✅ Done | Every scope check is guarded by `!auth.IsAdminFromContext(ctx)`. |
| Task 11 — Integration tests | Pending | Requires running DB. Handler changes are complete. |
| Task 12 — Documentation | ✅ Done | SEC_VALIDATION.md I3 updated. SECURITY.md scope section expanded with enforcement points. REST_API_SPEC.md scope behavior table added to API Keys section. |

## Task Ordering

```
Task 1  (scope resolution helper)
  ├── Task 2  (jobs: create, cancel, retry)
  ├── Task 3  (devices: list, get, update, revoke)
  ├── Task 4  (inventory)
  ├── Task 5  (jobs: list, get)
  ├── Task 6  (groups, tags, sites)
  ├── Task 7  (scheduled jobs, alert rules)
  ├── Task 8  (files — document no-op decision)
  ├── Task 9  (enrollment tokens — subset validation)
  └── Task 10 (admin bypass audit)
        └── Task 11 (integration tests — requires Tasks 2-10)
              └── Task 12 (documentation)
```

Tasks 2-10 can be parallelized after Task 1 is complete. Task 11 requires all handler changes. Task 12 is last.

---

## Out of Scope (for this plan)

- **OIDC-authenticated users:** OIDC sessions don't currently carry scope. This plan covers API key scope only (the only scope mechanism that exists today).
- **Enrollment token scope** at enrollment time: Already implemented (`enroll.go:applyScope`). This plan only addresses the subset-validation gap (Task 9).
- **Audit log scope filtering:** Audit log entries are tenant-wide. Scoped keys with `audit_log:read` see all entries. This is acceptable — the audit log is an administrative resource.
- **Agent endpoint scope:** Agent endpoints use mTLS identity, not API keys. Not affected by this issue.
