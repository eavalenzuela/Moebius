# File Transfer Specification

---

## Overview

File transfer is implemented as a job type (`file_transfer`). The server queues a transfer job targeting one or more devices; the agent pulls the file either directly from the server or from a third-party S3-compatible storage location. Transfer metadata — including integrity and signing requirements — is specified in the job payload. The agent never receives inbound connections; all transfers are agent-initiated pulls.

---

## Storage Backends

The server supports two file storage backends, configurable at the tenant level:

### Server-Side Storage (Default)
Files are stored on the server's local filesystem or a mounted volume. The server exposes a chunked download API that agents pull from directly using their mTLS client certificate.

### S3-Compatible Storage (Alternative)
Files are stored in an operator-supplied S3-compatible bucket (AWS S3, MinIO, Cloudflare R2, etc.). The server generates time-limited pre-signed URLs and includes them in the job payload. The agent pulls directly from the pre-signed URL — no traffic flows through the server for the file data itself.

**S3 configuration (per tenant):**
```json
{
  "storage": {
    "backend": "s3",
    "endpoint": "https://s3.amazonaws.com",
    "bucket": "acme-agent-files",
    "region": "us-east-1",
    "access_key_id": "...",
    "secret_access_key": "...",
    "presign_expiry_seconds": 3600
  }
}
```

Backend selection is transparent to the agent — the job payload always contains either a server download URL or a pre-signed external URL. The agent does not need to know which backend is in use.

---

## File Upload (Operator → Server)

Before a file transfer job can be created, the file must be uploaded to the server. Uploads use a chunked, resumable protocol.

### 1. Initiate Upload
```
POST /v1/files
Authorization: Bearer <api_key>
Content-Type: application/json
```

**Request:**
```json
{
  "filename": "deploy-package.tar.gz",
  "size_bytes": 104857600,
  "sha256": "e3b0c44298fc1c149afb...",
  "signature": "<base64-encoded signature>",
  "signature_key_id": "sigkey_01HZ...",
  "mime_type": "application/gzip"
}
```

**Response `201`:**
```json
{
  "file_id": "fil_01HZ...",
  "upload_id": "upl_01HZ...",
  "chunk_size_bytes": 5242880,
  "total_chunks": 20,
  "uploaded_chunks": [],
  "expires_at": "2026-03-29T12:00:00Z"
}
```

- `sha256` is required — the server verifies the full file checksum after all chunks are received
- `signature` and `signature_key_id` are optional — required only if the resulting transfer job will mandate signature verification
- Upload sessions expire after 24 hours of inactivity

---

### 2. Upload Chunk
```
PUT /v1/files/uploads/{upload_id}/chunks/{chunk_index}
Authorization: Bearer <api_key>
Content-Type: application/octet-stream
Content-Length: <chunk_size>
X-Chunk-SHA256: <sha256 of this chunk>
```

**Body:** Raw binary chunk data.

**Response `200`:**
```json
{
  "chunk_index": 0,
  "received": true,
  "uploaded_chunks": [0],
  "remaining_chunks": 19
}
```

- Chunks may be uploaded in any order
- Each chunk has its own SHA-256 header for per-chunk integrity verification
- Default chunk size: 5MB (configurable per server deployment)
- Failed or interrupted chunks can be re-uploaded — idempotent per chunk index

---

### 3. Complete Upload
```
POST /v1/files/uploads/{upload_id}/complete
Authorization: Bearer <api_key>
```

**Response `200`:**
```json
{
  "file_id": "fil_01HZ...",
  "filename": "deploy-package.tar.gz",
  "size_bytes": 104857600,
  "sha256": "e3b0c44298fc1c149afb...",
  "signature_verified": true,
  "storage_backend": "s3",
  "created_at": "2026-03-28T12:00:00Z"
}
```

The server assembles all chunks, verifies the full-file SHA-256, and (if provided) verifies the file signature against the registered signing key. If either check fails, the upload is rejected and the file record is deleted.

---

### Resume an Interrupted Upload
```
GET /v1/files/uploads/{upload_id}
```

**Response `200`:**
```json
{
  "upload_id": "upl_01HZ...",
  "file_id": "fil_01HZ...",
  "total_chunks": 20,
  "uploaded_chunks": [0, 1, 2, 5],
  "expires_at": "2026-03-29T12:00:00Z"
}
```

The client re-uploads only the missing chunks, then calls `/complete` again.

---

## File Management API

### List Files
```
GET /v1/files
```
**Query params:** `cursor`, `limit`, `search`

**Response `200`:** Paginated list of file metadata objects (no binary content).

---

### Get File Metadata
```
GET /v1/files/{file_id}
```

**Response `200`:**
```json
{
  "id": "fil_01HZ...",
  "filename": "deploy-package.tar.gz",
  "size_bytes": 104857600,
  "sha256": "e3b0c44298fc1c149afb...",
  "signature_key_id": "sigkey_01HZ...",
  "signature_verified": true,
  "mime_type": "application/gzip",
  "storage_backend": "s3",
  "created_by": "usr_01HZ...",
  "created_at": "2026-03-28T12:00:00Z"
}
```

---

### Delete File
```
DELETE /v1/files/{file_id}
```
Fails if the file is referenced by any pending or active transfer jobs.

**Response `204`:** No content.

---

## File Transfer Job

### Creating a Transfer Job
```
POST /v1/jobs
```

**Request:**
```json
{
  "type": "file_transfer",
  "target": {
    "device_ids": ["dev_01HZ..."],
    "group_ids": [],
    "tag_ids": [],
    "site_ids": []
  },
  "payload": {
    "file_id": "fil_01HZ...",
    "integrity": {
      "require_sha256": true,
      "require_signature": false
    },
    "on_complete": {
      "exec": "chmod +x /opt/agent-drop/deploy-package.tar.gz"
    }
  },
  "retry_policy": {
    "max_retries": 3,
    "retry_delay_seconds": 300
  }
}
```

**Payload fields:**

| Field | Required | Description |
|---|---|---|
| `file_id` | Yes | ID of a previously uploaded file |
| `integrity.require_sha256` | No (default: true) | Agent must verify SHA-256 after download |
| `integrity.require_signature` | No (default: false) | Agent must verify file signature; fails if file has no signature |
| `on_complete.exec` | No | Shell command to run on the agent after successful transfer and verification |

---

## Agent Transfer Flow

When the agent receives a `file_transfer` job via check-in:

```
Agent                                    Server / Storage
  │                                            │
  │  1. Receive file_transfer job              │
  │     via check-in response                 │
  │                                            │
  │  2. Pre-flight checks:                    │
  │     - Check free space on drop dir        │
  │     - Reject if file > 50% free space     │
  │       (if space check enabled)            │
  │                                            │
  │  3. Acknowledge job                        │
  │────────────────────────────────────────────►
  │                                            │
  │  4. Request download URL                   │
  │  GET /v1/files/{file_id}/download         │
  │────────────────────────────────────────────►
  │                                            │
  │  ◄── Download URL (server or presigned S3)─│
  │                                            │
  │  5. Pull file in chunks (Range requests)  │
  │  GET <download_url>                        │
  │  Range: bytes=0-5242879                   │
  │────────────────────────────────────────────►
  │  ◄── Chunk data ───────────────────────────│
  │  (repeat for each chunk)                  │
  │                                            │
  │  6. Verify SHA-256 of assembled file      │
  │     (if require_sha256: true)             │
  │                                            │
  │  7. Verify signature                       │
  │     (if require_signature: true)          │
  │                                            │
  │  8. Move file to drop directory            │
  │                                            │
  │  9. Execute on_complete command            │
  │     (if specified)                        │
  │                                            │
  │  10. Submit job result                     │
  │────────────────────────────────────────────►
```

### Download URL Endpoint (Server-Side Backend)
```
GET /v1/files/{file_id}/download
```
mTLS required. Returns a short-lived download URL (5 minute expiry) and chunk metadata.

**Response `200`:**
```json
{
  "url": "https://server/v1/files/fil_01HZ.../data",
  "size_bytes": 104857600,
  "chunk_size_bytes": 5242880,
  "total_chunks": 20,
  "sha256": "e3b0c44298fc1c149afb...",
  "signature": "<base64>",
  "signature_key_id": "sigkey_01HZ...",
  "expires_at": "2026-03-28T12:05:00Z"
}
```

For S3-compatible backend, `url` is a pre-signed URL pointing directly to the storage provider.

---

## Agent-Side Configuration

### Drop Directory
The agent has a configurable default drop directory. All transferred files land here unless overridden.

| Platform | Default Path |
|---|---|
| Linux | `/opt/agent/drop` |
| Windows | `C:\ProgramData\Agent\Drop` |

Configurable via agent config file or via server-pushed `config` block in check-in response.

### Free Space Check
Before beginning any transfer, the agent checks available disk space on the volume containing the drop directory.

| Setting | Default | Description |
|---|---|---|
| `storage.space_check_enabled` | `true` | Enable/disable the free space check |
| `storage.space_check_threshold` | `0.50` | Reject transfer if file size > this fraction of free space |

If the check fails, the job transitions to `FAILED` with error `insufficient_disk_space`. The operator can either free space on the device, disable the check via agent config, or lower the threshold.

The space check threshold is also configurable server-side per-job in the payload, allowing operators to override it for specific transfers:

```json
"payload": {
  "file_id": "fil_01HZ...",
  "storage": {
    "space_check_enabled": true,
    "space_check_threshold": 0.25
  }
}
```

Job-level config takes precedence over agent config.

---

## Signing Keys

File signatures allow operators to verify that transferred files come from a trusted source. Signing is optional but recommended for executable payloads and agent updates.

### Register Signing Key
```
POST /v1/signing-keys
```
**Request:**
```json
{
  "name": "Release signing key",
  "public_key": "<PEM-encoded Ed25519 public key>"
}
```

**Response `201`:**
```json
{
  "id": "sigkey_01HZ...",
  "name": "Release signing key",
  "algorithm": "ed25519",
  "fingerprint": "SHA256:...",
  "created_at": "2026-03-28T12:00:00Z"
}
```

### List Signing Keys
```
GET /v1/signing-keys
```

### Delete Signing Key
```
DELETE /v1/signing-keys/{key_id}
```
Fails if the key is referenced by any stored files.

### Signing Algorithm
Ed25519 is the only supported signing algorithm. Operators sign files out-of-band using their private key before upload. The server and agent only ever hold and verify public keys — private signing keys are never transmitted.

---

## Transfer Job States

File transfer jobs follow the standard job lifecycle with these additional failure reasons:

| Error Code | Meaning |
|---|---|
| `insufficient_disk_space` | File exceeds free space threshold on agent |
| `checksum_mismatch` | Downloaded file SHA-256 does not match job metadata |
| `signature_verification_failed` | File signature invalid or missing when required |
| `download_url_expired` | Agent did not begin download before URL expiry |
| `chunk_reassembly_failed` | Chunks could not be reassembled into a valid file |
| `on_complete_failed` | Post-transfer command exited non-zero |

`on_complete_failed` is treated as a job failure — the file is retained in the drop directory but the job result records the command's exit code and stderr.

---

## Database Additions

```sql
files (
  id                  uuid primary key,
  tenant_id           uuid not null references tenants(id),
  filename            text not null,
  size_bytes          bigint not null,
  sha256              text not null,
  signature           text,
  signature_key_id    uuid references signing_keys(id),
  signature_verified  bool not null default false,
  mime_type           text,
  storage_backend     text not null,  -- 'server' | 's3'
  storage_path        text not null,  -- local path or S3 key
  created_by          uuid references users(id),
  created_at          timestamptz not null
)

file_uploads (
  id              uuid primary key,
  file_id         uuid not null references files(id),
  tenant_id       uuid not null references tenants(id),
  chunk_size_bytes int not null,
  total_chunks    int not null,
  uploaded_chunks int[] not null default '{}',
  completed_at    timestamptz,
  expires_at      timestamptz not null,
  created_at      timestamptz not null
)

signing_keys (
  id          uuid primary key,
  tenant_id   uuid not null references tenants(id),
  name        text not null,
  algorithm   text not null default 'ed25519',
  public_key  text not null,
  fingerprint text not null,
  created_by  uuid references users(id),
  created_at  timestamptz not null
)
```

---

## Security Considerations

- **Agent pulls only** — no inbound connections to agents; transfer direction cannot be reversed
- **mTLS on server downloads** — agent identity is verified before a download URL is issued
- **Pre-signed URL expiry** — S3 URLs expire in 1 hour; server-side download URLs expire in 5 minutes
- **Per-chunk integrity** — SHA-256 verified per chunk during upload, and full-file SHA-256 verified by agent after reassembly
- **Signing is end-to-end** — private signing keys never touch the server; only public keys are registered
- **Drop directory is isolated** — files land in a dedicated directory, not arbitrary paths, limiting blast radius if a transfer job is maliciously crafted
- **CDM compliance** — file transfer jobs are gated by CDM in the same way as exec jobs; no transfers occur without an active CDM session if CDM is enabled
