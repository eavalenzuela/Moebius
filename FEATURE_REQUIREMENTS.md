## Agent

* Self-registration with server on first run
* Local encryption keys generated on install, stored to TPM/ keyring/ WinCredentialMgr
* Persistent heartbeat / check-in
* Hardware + software inventory collection
* OS metadata collection

* Execute server-dispatched commands, return output + exit code
* Package install/uninstall/update via native package manager
* File transfer (receive arbitrary files from server)
* Agent self-update

* Secure, authenticated communication with server
    * Certificate-based, per-device auth
* Local logging + log shipping to server
* Customer Device Mode (CDM):
    * Configurable at install time, toggleable via local UI + CLI
    * Holds inbound execution jobs pending local consent
    * Local user grants timed sessions via UI or CLI
    * In-flight jobs complete on expiry; no new jobs accepted after
    * Session revocable early by local user
    * Session state is local-authoritative
    * Audit log on grant, revoke, expiry, and per-job execution during session
    * Heartbeat, inventory, and telemetry always flow regardless of CDM state


## Local Agent Management (UI + CLI)

* View agent status, current config, and CDM state
* Toggle CDM on/off
* Grant/revoke CDM sessions with configurable duration
* View pending inbound job requests
* View local audit log
* Secured (auth-gated if exposed as local web service)

## Server

* Agent registration + identity management
* Device inventory store + query API
* Live agent status tracking
* Command dispatch + result collection
* Package deployment jobs (single device, group, or all)
* File transfer to endpoints
* Job queue + execution history
* Scheduled jobs
* Alerting + webhooks
* Agent grouping + tagging + site labels
* REST API for UI + third-party integrations

* RBAC:
    * Predefined roles (Super Admin, Tenant Admin, Operator, Technician, Viewer)
    * Custom roles via composable permissions, defined per tenant
    * Scoping across tenant, group/tag, site, and individual device
    * All enforcement server-side
    * Admin API keys + scoped API keys bound to role + scope
    * RBAC changes and key lifecycle events audit-logged

* SSO/OIDC integration (roles assignable to SSO identities)
* Multi-tenancy with tenant isolation

## Web UI

* Device list with live status, search, filter, group/tag views
* Device detail view (inventory, jobs, logs, CDM state)
* Job creation + monitoring
* File transfer initiation
* Scheduled job management
* Alert configuration
* User + org management
