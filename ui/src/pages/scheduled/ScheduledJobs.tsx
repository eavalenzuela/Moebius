import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { PaginatedResponse, ScheduledJob } from '../../types/api';

export default function ScheduledJobs() {
  const { data, loading, refetch } = useFetch<PaginatedResponse<ScheduledJob>>('/scheduled-jobs?limit=100');
  const [showCreate, setShowCreate] = useState(false);

  const jobs = data?.data ?? [];

  async function toggle(id: string, enabled: boolean) {
    await api.post(`/scheduled-jobs/${id}/${enabled ? 'disable' : 'enable'}`);
    refetch();
  }

  async function remove(id: string) {
    await api.del(`/scheduled-jobs/${id}`);
    refetch();
  }

  return (
    <div>
      <div className="page-header">
        <h2>Scheduled Jobs</h2>
        <button className="btn btn-primary" onClick={() => setShowCreate(!showCreate)}>
          {showCreate ? 'Cancel' : 'Create Schedule'}
        </button>
      </div>

      {showCreate && <CreateSchedule onDone={() => { setShowCreate(false); refetch(); }} />}

      {loading ? <p>Loading...</p> : (
        <table>
          <thead>
            <tr><th>Name</th><th>Type</th><th>Cron</th><th>Enabled</th><th>Last Run</th><th>Next Run</th><th>Actions</th></tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id}>
                <td>{j.name}</td>
                <td>{j.job_type}</td>
                <td><code>{j.cron_expr}</code></td>
                <td>{j.enabled ? 'Yes' : 'No'}</td>
                <td>{j.last_run_at ? new Date(j.last_run_at).toLocaleString() : '—'}</td>
                <td>{j.next_run_at ? new Date(j.next_run_at).toLocaleString() : '—'}</td>
                <td>
                  <button className="btn-sm" onClick={() => toggle(j.id, j.enabled)}>
                    {j.enabled ? 'Disable' : 'Enable'}
                  </button>
                  <button className="btn-sm btn-danger" onClick={() => remove(j.id)}>Delete</button>
                </td>
              </tr>
            ))}
            {jobs.length === 0 && <tr><td colSpan={7} className="empty">No scheduled jobs.</td></tr>}
          </tbody>
        </table>
      )}
    </div>
  );
}

function CreateSchedule({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('');
  const [jobType, setJobType] = useState('exec');
  const [cronExpr, setCronExpr] = useState('0 * * * *');
  const [targetJson, setTargetJson] = useState('{"group_ids": []}');
  const [payloadJson, setPayloadJson] = useState('{}');
  const [error, setError] = useState('');

  const CRON_PRESETS = [
    { label: 'Every hour', value: '0 * * * *' },
    { label: 'Every day 2 AM', value: '0 2 * * *' },
    { label: 'Every 15 min', value: '*/15 * * * *' },
    { label: 'Weekly Monday 6 AM', value: '0 6 * * 1' },
    { label: 'Monthly 1st 3 AM', value: '0 3 1 * *' },
  ];

  async function submit() {
    setError('');
    try {
      await api.post('/scheduled-jobs', {
        name,
        job_type: jobType,
        cron_expr: cronExpr,
        target: JSON.parse(targetJson),
        payload: JSON.parse(payloadJson),
        enabled: true,
      });
      onDone();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to create');
    }
  }

  return (
    <div className="form card">
      {error && <p className="error">{error}</p>}
      <label>Name <input value={name} onChange={(e) => setName(e.target.value)} required /></label>
      <label>
        Job Type
        <select value={jobType} onChange={(e) => setJobType(e.target.value)}>
          <option value="exec">exec</option>
          <option value="package_install">package_install</option>
          <option value="package_update">package_update</option>
          <option value="inventory_full">inventory_full</option>
        </select>
      </label>
      <label>
        Cron Expression
        <div className="inline-form">
          <input value={cronExpr} onChange={(e) => setCronExpr(e.target.value)} />
          <select onChange={(e) => { if (e.target.value) setCronExpr(e.target.value); }}>
            <option value="">Presets...</option>
            {CRON_PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
          </select>
        </div>
      </label>
      <label>Target (JSON) <textarea rows={2} value={targetJson} onChange={(e) => setTargetJson(e.target.value)} /></label>
      <label>Payload (JSON) <textarea rows={2} value={payloadJson} onChange={(e) => setPayloadJson(e.target.value)} /></label>
      <button className="btn btn-primary" onClick={submit}>Create</button>
    </div>
  );
}
