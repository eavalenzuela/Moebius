import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useFetch } from '../../hooks/useApi';
import StatusBadge from '../../components/StatusBadge';
import TimeAgo from '../../components/TimeAgo';
import type { Device, DeviceInventory, Job, DeviceLog, PaginatedResponse } from '../../types/api';

type Tab = 'overview' | 'inventory' | 'jobs' | 'logs';

export default function DeviceDetail() {
  const { deviceId } = useParams<{ deviceId: string }>();
  const { data: device, loading, error } = useFetch<Device>(`/devices/${deviceId}`);
  const [tab, setTab] = useState<Tab>('overview');

  if (loading) return <p>Loading...</p>;
  if (error) return <p className="error">{error}</p>;
  if (!device) return <p>Device not found.</p>;

  return (
    <div>
      <div className="page-header">
        <h2>{device.hostname}</h2>
        <StatusBadge status={device.status} />
      </div>

      <div className="tabs">
        {(['overview', 'inventory', 'jobs', 'logs'] as Tab[]).map((t) => (
          <button key={t} className={tab === t ? 'tab active' : 'tab'} onClick={() => setTab(t)}>
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'overview' && <OverviewTab device={device} />}
      {tab === 'inventory' && <InventoryTab deviceId={device.id} />}
      {tab === 'jobs' && <JobsTab deviceId={device.id} />}
      {tab === 'logs' && <LogsTab deviceId={device.id} />}
    </div>
  );
}

function OverviewTab({ device }: { device: Device }) {
  return (
    <div className="detail-grid">
      <dl>
        <dt>ID</dt><dd>{device.id}</dd>
        <dt>OS</dt><dd>{device.os} {device.os_version}</dd>
        <dt>Architecture</dt><dd>{device.arch}</dd>
        <dt>Agent Version</dt><dd>{device.agent_version}</dd>
        <dt>Registered</dt><dd>{new Date(device.registered_at).toLocaleString()}</dd>
        <dt>Last Seen</dt><dd><TimeAgo ts={device.last_seen_at} /></dd>
        <dt>CDM Enabled</dt><dd>{device.cdm_enabled ? 'Yes' : 'No'}</dd>
        {device.cdm_session_active && (
          <>
            <dt>CDM Session Expires</dt>
            <dd><TimeAgo ts={device.cdm_session_expires_at} /></dd>
          </>
        )}
      </dl>
      <div>
        <h4>Groups</h4>
        {device.groups.length ? device.groups.map((g) => <span key={g.id} className="chip">{g.name}</span>) : <span className="muted">None</span>}
        <h4>Tags</h4>
        {device.tags.length ? device.tags.map((t) => <span key={t.id} className="chip">{t.name}</span>) : <span className="muted">None</span>}
        <h4>Sites</h4>
        {device.sites.length ? device.sites.map((s) => <span key={s.id} className="chip">{s.name}</span>) : <span className="muted">None</span>}
      </div>
    </div>
  );
}

function InventoryTab({ deviceId }: { deviceId: string }) {
  const { data, loading } = useFetch<DeviceInventory>(`/devices/${deviceId}/inventory`);
  if (loading) return <p>Loading inventory...</p>;
  if (!data) return <p className="muted">No inventory data.</p>;

  return (
    <div>
      <h3>Hardware</h3>
      <dl>
        <dt>CPU</dt><dd>{JSON.stringify(data.hardware.cpu)}</dd>
        <dt>RAM</dt><dd>{data.hardware.ram_mb} MB</dd>
        <dt>Disks</dt><dd>{data.hardware.disks.length} disk(s)</dd>
        <dt>NICs</dt><dd>{data.hardware.network_interfaces.length} interface(s)</dd>
      </dl>
      <h3>Packages ({data.packages.length})</h3>
      <table>
        <thead><tr><th>Name</th><th>Version</th><th>Manager</th></tr></thead>
        <tbody>
          {data.packages.map((p, i) => (
            <tr key={i}><td>{p.name}</td><td>{p.version}</td><td>{p.manager}</td></tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function JobsTab({ deviceId }: { deviceId: string }) {
  const { data, loading } = useFetch<PaginatedResponse<Job>>(`/devices/${deviceId}/jobs?limit=50`);
  if (loading) return <p>Loading jobs...</p>;
  const jobs = data?.data ?? [];

  return (
    <table>
      <thead><tr><th>ID</th><th>Type</th><th>Status</th><th>Created</th></tr></thead>
      <tbody>
        {jobs.map((j) => (
          <tr key={j.id}>
            <td><Link to={`/jobs/${j.id}`}>{j.id.slice(0, 12)}...</Link></td>
            <td>{j.type}</td>
            <td><StatusBadge status={j.status} /></td>
            <td><TimeAgo ts={j.created_at} /></td>
          </tr>
        ))}
        {jobs.length === 0 && <tr><td colSpan={4} className="empty">No jobs.</td></tr>}
      </tbody>
    </table>
  );
}

function LogsTab({ deviceId }: { deviceId: string }) {
  const { data, loading } = useFetch<PaginatedResponse<DeviceLog>>(`/devices/${deviceId}/logs?limit=100`);
  if (loading) return <p>Loading logs...</p>;
  const logs = data?.data ?? [];

  return (
    <div className="log-viewer">
      {logs.length === 0 && <p className="muted">No logs.</p>}
      {logs.map((l) => (
        <div key={l.id} className={`log-entry log-${l.level}`}>
          <span className="log-ts">{new Date(l.timestamp).toLocaleString()}</span>
          <span className="log-level">{l.level}</span>
          <span className="log-msg">{l.message}</span>
        </div>
      ))}
    </div>
  );
}
