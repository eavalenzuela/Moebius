import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { PaginatedResponse, EnrollmentToken } from '../../types/api';

export default function EnrollmentTokens() {
  const { data, refetch } = useFetch<PaginatedResponse<EnrollmentToken>>('/enrollment-tokens?limit=200');
  const [expiresHours, setExpiresHours] = useState(24);
  const [newToken, setNewToken] = useState('');
  const [error, setError] = useState('');

  const tokens = data?.data ?? [];

  async function create() {
    setError('');
    setNewToken('');
    try {
      const res = await api.post<EnrollmentToken>('/enrollment-tokens', {
        expires_in_seconds: expiresHours * 3600,
      });
      setNewToken(res.token ?? '');
      refetch();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed');
    }
  }

  async function revoke(id: string) {
    await api.del(`/enrollment-tokens/${id}`);
    refetch();
  }

  return (
    <div>
      <h3>Enrollment Tokens</h3>

      <div className="inline-form">
        <label>
          Expires in (hours)
          <input type="number" value={expiresHours} onChange={(e) => setExpiresHours(Number(e.target.value))} min={1} />
        </label>
        <button onClick={create}>Create Token</button>
      </div>
      {error && <p className="error">{error}</p>}
      {newToken && (
        <div className="alert alert-success">
          <strong>Enrollment Token (copy now, shown once):</strong>
          <code>{newToken}</code>
        </div>
      )}

      <table>
        <thead><tr><th>ID</th><th>Used</th><th>Expires</th><th>Created</th><th></th></tr></thead>
        <tbody>
          {tokens.map((t) => (
            <tr key={t.id}>
              <td className="muted">{t.id}</td>
              <td>{t.used_at ? new Date(t.used_at).toLocaleString() : '—'}</td>
              <td>{new Date(t.expires_at).toLocaleString()}</td>
              <td>{new Date(t.created_at).toLocaleDateString()}</td>
              <td><button className="btn-sm btn-danger" onClick={() => revoke(t.id)}>Revoke</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
