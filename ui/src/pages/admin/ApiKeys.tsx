import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import TimeAgo from '../../components/TimeAgo';
import type { PaginatedResponse, ApiKey } from '../../types/api';

export default function ApiKeys() {
  const { data, refetch } = useFetch<PaginatedResponse<ApiKey>>('/api-keys?limit=200');
  const [name, setName] = useState('');
  const [newKey, setNewKey] = useState('');
  const [error, setError] = useState('');

  const keys = data?.data ?? [];

  async function create() {
    setError('');
    setNewKey('');
    try {
      const res = await api.post<ApiKey>('/api-keys', { name, is_admin: false });
      setNewKey(res.key ?? '');
      setName('');
      refetch();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed');
    }
  }

  async function revoke(id: string) {
    await api.del(`/api-keys/${id}`);
    refetch();
  }

  return (
    <div>
      <h3>API Keys</h3>

      <div className="inline-form">
        <input placeholder="Key name" value={name} onChange={(e) => setName(e.target.value)} />
        <button onClick={create}>Create</button>
      </div>
      {error && <p className="error">{error}</p>}
      {newKey && (
        <div className="alert alert-success">
          <strong>New API Key (copy now, shown once):</strong>
          <code>{newKey}</code>
        </div>
      )}

      <table>
        <thead><tr><th>Name</th><th>Admin</th><th>Last Used</th><th>Expires</th><th>Created</th><th></th></tr></thead>
        <tbody>
          {keys.map((k) => (
            <tr key={k.id}>
              <td>{k.name}</td>
              <td>{k.is_admin ? 'Yes' : 'No'}</td>
              <td><TimeAgo ts={k.last_used_at} /></td>
              <td>{k.expires_at ? new Date(k.expires_at).toLocaleDateString() : 'Never'}</td>
              <td>{new Date(k.created_at).toLocaleDateString()}</td>
              <td><button className="btn-sm btn-danger" onClick={() => revoke(k.id)}>Revoke</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
