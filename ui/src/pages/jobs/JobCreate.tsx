import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../../api/client';

export default function JobCreate() {
  const navigate = useNavigate();
  const [type, setType] = useState('exec');
  const [targetMode, setTargetMode] = useState<'devices' | 'groups' | 'tags' | 'sites'>('devices');
  const [targetIds, setTargetIds] = useState('');
  const [command, setCommand] = useState('');
  const [timeout, setTimeout] = useState(120);
  const [maxRetries, setMaxRetries] = useState(0);
  const [payloadJson, setPayloadJson] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError('');

    try {
      const ids = targetIds.split(',').map((s) => s.trim()).filter(Boolean);
      const target: Record<string, string[]> = { device_ids: [], group_ids: [], tag_ids: [], site_ids: [] };
      target[`${targetMode === 'devices' ? 'device' : targetMode.slice(0, -1)}_ids`] = ids;

      let payload: Record<string, unknown>;
      if (type === 'exec') {
        payload = { command, timeout_seconds: timeout };
      } else if (payloadJson) {
        payload = JSON.parse(payloadJson);
      } else {
        payload = {};
      }

      const res = await api.post<{ job_ids: string[] }>('/jobs', {
        type,
        target,
        payload,
        retry_policy: { max_retries: maxRetries },
      });

      if (res.job_ids.length === 1) {
        navigate(`/jobs/${res.job_ids[0]}`);
      } else {
        navigate('/jobs');
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to create job');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div>
      <h2>Create Job</h2>
      {error && <p className="error">{error}</p>}

      <form onSubmit={handleSubmit} className="form">
        <label>
          Type
          <select value={type} onChange={(e) => setType(e.target.value)}>
            <option value="exec">Command (exec)</option>
            <option value="package_install">Package Install</option>
            <option value="package_remove">Package Remove</option>
            <option value="package_update">Package Update</option>
            <option value="file_transfer">File Transfer</option>
            <option value="inventory_full">Inventory Full</option>
            <option value="agent_update">Agent Update</option>
          </select>
        </label>

        <label>
          Target
          <select value={targetMode} onChange={(e) => setTargetMode(e.target.value as typeof targetMode)}>
            <option value="devices">Devices</option>
            <option value="groups">Groups</option>
            <option value="tags">Tags</option>
            <option value="sites">Sites</option>
          </select>
        </label>

        <label>
          {targetMode.charAt(0).toUpperCase() + targetMode.slice(1)} IDs (comma-separated)
          <input value={targetIds} onChange={(e) => setTargetIds(e.target.value)} placeholder="id1, id2, ..." required />
        </label>

        {type === 'exec' ? (
          <>
            <label>
              Command
              <input value={command} onChange={(e) => setCommand(e.target.value)} placeholder="apt-get update" required />
            </label>
            <label>
              Timeout (seconds)
              <input type="number" value={timeout} onChange={(e) => setTimeout(Number(e.target.value))} min={1} />
            </label>
          </>
        ) : (
          <label>
            Payload (JSON)
            <textarea rows={4} value={payloadJson} onChange={(e) => setPayloadJson(e.target.value)} placeholder="{}" />
          </label>
        )}

        <label>
          Max Retries
          <input type="number" value={maxRetries} onChange={(e) => setMaxRetries(Number(e.target.value))} min={0} />
        </label>

        <button type="submit" className="btn btn-primary" disabled={submitting}>
          {submitting ? 'Creating...' : 'Create Job'}
        </button>
      </form>
    </div>
  );
}
