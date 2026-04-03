import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { PaginatedResponse, AlertRule } from '../../types/api';

export default function AlertRules() {
  const { data, refetch } = useFetch<PaginatedResponse<AlertRule>>('/alert-rules?limit=200');
  const [showCreate, setShowCreate] = useState(false);
  const rules = data?.data ?? [];

  async function toggle(id: string, enabled: boolean) {
    await api.post(`/alert-rules/${id}/${enabled ? 'disable' : 'enable'}`);
    refetch();
  }

  async function remove(id: string) {
    await api.del(`/alert-rules/${id}`);
    refetch();
  }

  return (
    <div>
      <div className="page-header">
        <h3>Alert Rules</h3>
        <button className="btn btn-primary" onClick={() => setShowCreate(!showCreate)}>
          {showCreate ? 'Cancel' : 'Create Rule'}
        </button>
      </div>

      {showCreate && <CreateAlert onDone={() => { setShowCreate(false); refetch(); }} />}

      <table>
        <thead><tr><th>Name</th><th>Condition</th><th>Enabled</th><th>Actions</th></tr></thead>
        <tbody>
          {rules.map((r) => (
            <tr key={r.id}>
              <td>{r.name}</td>
              <td><code>{JSON.stringify(r.condition)}</code></td>
              <td>{r.enabled ? 'Yes' : 'No'}</td>
              <td>
                <button className="btn-sm" onClick={() => toggle(r.id, r.enabled)}>
                  {r.enabled ? 'Disable' : 'Enable'}
                </button>
                <button className="btn-sm btn-danger" onClick={() => remove(r.id)}>Delete</button>
              </td>
            </tr>
          ))}
          {rules.length === 0 && <tr><td colSpan={4} className="empty">No alert rules.</td></tr>}
        </tbody>
      </table>
    </div>
  );
}

function CreateAlert({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('');
  const [condType, setCondType] = useState('agent_offline');
  const [threshold, setThreshold] = useState(5);
  const [webhooks, setWebhooks] = useState('');
  const [emails, setEmails] = useState('');
  const [error, setError] = useState('');

  async function submit() {
    setError('');
    try {
      await api.post('/alert-rules', {
        name,
        condition: { type: condType, threshold_minutes: threshold },
        channels: {
          webhooks: webhooks.split(',').map((s) => s.trim()).filter(Boolean),
          emails: emails.split(',').map((s) => s.trim()).filter(Boolean),
        },
        enabled: true,
      });
      onDone();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed');
    }
  }

  return (
    <div className="form card">
      {error && <p className="error">{error}</p>}
      <label>Name <input value={name} onChange={(e) => setName(e.target.value)} /></label>
      <label>
        Condition Type
        <select value={condType} onChange={(e) => setCondType(e.target.value)}>
          <option value="agent_offline">Agent Offline</option>
          <option value="job_failure_rate">Job Failure Rate</option>
        </select>
      </label>
      <label>Threshold (minutes) <input type="number" value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} min={1} /></label>
      <label>Webhook URLs (comma-separated) <input value={webhooks} onChange={(e) => setWebhooks(e.target.value)} /></label>
      <label>Email addresses (comma-separated) <input value={emails} onChange={(e) => setEmails(e.target.value)} /></label>
      <button className="btn btn-primary" onClick={submit}>Create Rule</button>
    </div>
  );
}
