# Agent Authentication Model

---

## Overview

Agents authenticate to the server using mutual TLS (mTLS). Each agent holds a unique client certificate signed by the server's internal CA. The private key is generated on the device at enrollment time and never transmitted. All subsequent communication is authenticated at the TLS layer — no bearer tokens, no API keys for agents.

---

## PKI Structure

```
Root CA (offline, air-gapped recommended)
  └── Intermediate CA (online, held by server)
        ├── Server TLS certificate
        ├── Agent cert (device-001)
        ├── Agent cert (device-002)
        └── ...
```

- **Root CA** — self-signed, long-lived (10yr), used only to sign the Intermediate CA. Should be kept offline in production and SaaS deployments.
- **Intermediate CA** — held by the server, used to sign agent CSRs and the server's own TLS cert. Rotated periodically (e.g. annually).
- **Agent certificates** — short-lived (default: 90 days), automatically renewed. Contain the `agent_id` in the Subject or SAN for identity binding.

---

## Enrollment Flow

```
Operator                  Server                    Agent
   │                         │                         │
   │  1. Create enrollment   │                         │
   │     token (scoped,      │                         │
   │     time-limited)       │                         │
   │─────────────────────────►                         │
   │                         │                         │
   │  2. Distribute token    │                         │
   │     + server CA cert    │                         │
   │     to device           │                         │
   │─────────────────────────────────────────────────►│
   │                         │                         │
   │                         │  3. Agent generates     │
   │                         │     keypair + CSR       │
   │                         │◄────────────────────────│
   │                         │                         │
   │                         │  4. Server validates    │
   │                         │     enrollment token,   │
   │                         │     signs CSR,          │
   │                         │     returns cert +      │
   │                         │     agent_id            │
   │                         │─────────────────────────►
   │                         │                         │
   │                         │  5. Agent stores cert   │
   │                         │     + key, begins       │
   │                         │     check-in loop       │
```

### Enrollment Token Properties

- Single-use — invalidated immediately on successful enrollment
- Time-limited (configurable, default: 24h)
- Optionally pre-scoped to a tenant, group, site, or tag — device inherits scope metadata on registration
- Stored hashed server-side; never recoverable after issuance
- Audit-logged on creation, use, and expiry

### Enrollment Endpoint

Unauthenticated, enrollment-token-gated:

```
POST /v1/agents/enroll
Content-Type: application/json
```

**Request:**
```json
{
  "enrollment_token": "enr_01HZ...",
  "csr": "<PEM-encoded CSR>",
  "hostname": "workstation-42",
  "os": "linux",
  "os_version": "Ubuntu 24.04",
  "arch": "amd64",
  "agent_version": "1.4.2"
}
```

**Response:**
```json
{
  "agent_id": "agt_01HZ...",
  "certificate": "<PEM-encoded signed cert>",
  "ca_chain": "<PEM-encoded intermediate + root>",
  "poll_interval_seconds": 30
}
```

---

## Ongoing Authentication

All post-enrollment communication uses mTLS. The agent presents its client certificate on every request. The server validates:

1. Certificate is signed by the trusted Intermediate CA
2. Certificate is not expired
3. Certificate is not revoked (checked against CRL or OCSP)
4. `agent_id` in the certificate matches a known, active device record

No session tokens, no bearer headers — identity is entirely in the TLS handshake.

---

## Certificate Renewal

Agent certificates are short-lived (default: 90 days). Renewal is automatic:

```
Agent                          Server
  │                               │
  │  (cert expiry within 30 days) │
  │                               │
  │  POST /v1/agents/renew        │
  │  [mTLS with current cert]     │
  │  Body: new CSR                │
  ├──────────────────────────────►│
  │                               │  Validates current cert
  │                               │  is still trusted + not revoked
  │                               │  Signs new CSR
  │◄──────────────────────────────│
  │  New cert in response         │
  │                               │
  │  Swaps to new cert on         │
  │  next check-in                │
```

- Agent begins attempting renewal when cert has ≤ 30 days remaining
- Current cert remains valid until expiry — renewal never causes downtime
- If renewal fails repeatedly and the cert expires, agent falls back to re-enrollment
- Renewal is audit-logged

---

## Revocation & Re-enrollment

### Automatic Re-enrollment

Triggers when:
- Agent cert has expired and renewal has failed
- Agent receives a `401` with `reason: cert_revoked` or `reason: cert_expired` from the server

On automatic re-enrollment, the agent:
1. Generates a new keypair
2. Attempts to use a cached enrollment token (if one was pre-provisioned)
3. If no cached token is available, enters a **pending re-enrollment state** — continues attempting check-in, surfaces a re-enrollment required alert locally and in the server UI

### Manual Re-enrollment

Required when:
- No cached enrollment token is available
- Operator has explicitly revoked the agent and wants human confirmation before re-admitting the device
- The device has been reimaged or the agent reinstalled

### Revocation

Operator-initiated via the server UI or API. On revocation:
- Certificate is added to the CRL / OCSP responder immediately
- Device record is marked `revoked` in the database
- All pending jobs for the device are cancelled
- Re-enrollment requires a new operator-issued enrollment token
- Revocation is audit-logged

---

## Database Schema

```sql
agent_certificates (
  id                uuid primary key,
  device_id         uuid not null references devices(id),
  serial_number     text not null unique,
  fingerprint       text not null unique,
  issued_at         timestamptz not null,
  expires_at        timestamptz not null,
  revoked_at        timestamptz,
  revocation_reason text
)

enrollment_tokens (
  id          uuid primary key,
  tenant_id   uuid not null references tenants(id),
  token_hash  text not null unique,
  created_by  uuid not null references users(id),
  scope       jsonb,
  used_at     timestamptz,
  expires_at  timestamptz not null,
  created_at  timestamptz not null
)
```

---

## Security Properties

| Property | How it's achieved |
|---|---|
| Private key never leaves device | Agent generates keypair locally; only CSR is transmitted |
| Per-device identity | Each agent has a unique cert with `agent_id` in SAN |
| Short blast radius on compromise | 90-day cert lifetime limits exposure window |
| Immediate revocation | CRL/OCSP checked on every connection |
| Enrollment is gated | Single-use, time-limited tokens; no open registration |
| Audit trail | Every enrollment, renewal, and revocation is logged |
| Server impersonation prevented | Agent validates server cert against CA chain at enrollment |
