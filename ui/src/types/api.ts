// Pagination envelope returned by all list endpoints
export interface PaginatedResponse<T> {
  data: T[];
  pagination: {
    next_cursor: string;
    has_more: boolean;
    limit: number;
  };
}

export interface Device {
  id: string;
  tenant_id: string;
  hostname: string;
  os: string;
  os_version: string;
  arch: string;
  agent_version: string;
  status: string;
  last_seen_at: string | null;
  registered_at: string;
  cdm_enabled: boolean;
  cdm_session_active: boolean;
  cdm_session_expires_at: string | null;
  groups: Group[];
  tags: Tag[];
  sites: Site[];
}

export interface DeviceInventory {
  hardware: {
    cpu: Record<string, unknown>;
    ram_mb: number;
    disks: unknown[];
    network_interfaces: unknown[];
  };
  packages: InventoryPackage[];
  collected_at: string;
}

export interface InventoryPackage {
  name: string;
  version: string;
  manager: string;
}

export interface Job {
  id: string;
  tenant_id: string;
  device_id: string;
  parent_job_id: string | null;
  type: string;
  status: string;
  payload: Record<string, unknown>;
  retry_policy: Record<string, unknown> | null;
  retry_count: number;
  max_retries: number;
  last_error: string | null;
  created_by: string | null;
  created_at: string;
  dispatched_at: string | null;
  acknowledged_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  result?: JobResult;
}

export interface JobResult {
  exit_code: number;
  stdout: string;
  stderr: string;
}

export interface ScheduledJob {
  id: string;
  tenant_id: string;
  name: string;
  job_type: string;
  payload: Record<string, unknown>;
  target: Record<string, unknown>;
  cron_expr: string;
  retry_policy: Record<string, unknown> | null;
  enabled: boolean;
  last_run_at: string | null;
  next_run_at: string | null;
}

export interface Group {
  id: string;
  tenant_id: string;
  name: string;
}

export interface Tag {
  id: string;
  tenant_id: string;
  name: string;
}

export interface Site {
  id: string;
  tenant_id: string;
  name: string;
  location: string;
}

export interface User {
  id: string;
  tenant_id: string;
  email: string;
  role_id: string | null;
  sso_subject: string | null;
  created_at: string;
}

export interface Role {
  id: string;
  tenant_id: string | null;
  name: string;
  permissions: string[];
  is_custom: boolean;
}

export interface ApiKey {
  id: string;
  tenant_id: string;
  name: string;
  key?: string;
  is_admin: boolean;
  role_id: string | null;
  scope: Record<string, unknown> | null;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export interface EnrollmentToken {
  id: string;
  tenant_id: string;
  token?: string;
  scope: Record<string, unknown> | null;
  used_at: string | null;
  expires_at: string;
  created_at: string;
}

export interface AlertRule {
  id: string;
  tenant_id: string;
  name: string;
  condition: Record<string, unknown>;
  channels: Record<string, unknown>;
  enabled: boolean;
}

export interface AuditEntry {
  id: string;
  actor_id: string;
  actor_type: string;
  action: string;
  resource_type: string;
  resource_id: string | null;
  metadata: Record<string, unknown> | null;
  ip_address: string | null;
  created_at: string;
}

export interface Tenant {
  id: string;
  name: string;
  slug: string;
  config: Record<string, unknown> | null;
}

export interface DeviceLog {
  id: string;
  timestamp: string;
  level: string;
  message: string;
}

export interface FileRecord {
  id: string;
  tenant_id: string;
  filename: string;
  size_bytes: number;
  sha256: string;
  mime_type: string | null;
  storage_backend: string;
  created_at: string;
}

export interface FileUpload {
  id: string;
  file_id: string;
  chunk_size_bytes: number;
  total_chunks: number;
  uploaded_chunks: number[];
  completed_at: string | null;
}
