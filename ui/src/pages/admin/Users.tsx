import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { PaginatedResponse, User, Role } from '../../types/api';

export default function Users() {
  const { data, refetch } = useFetch<PaginatedResponse<User>>('/users?limit=200');
  const { data: rolesData } = useFetch<PaginatedResponse<Role>>('/roles?limit=200');
  const [email, setEmail] = useState('');
  const [roleId, setRoleId] = useState('');
  const [error, setError] = useState('');

  const users = data?.data ?? [];
  const roles = rolesData?.data ?? [];

  async function invite() {
    setError('');
    try {
      await api.post('/users/invite', { email, role_id: roleId || undefined });
      setEmail('');
      refetch();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed');
    }
  }

  async function deactivate(id: string) {
    await api.post(`/users/${id}/deactivate`);
    refetch();
  }

  return (
    <div>
      <h3>Invite User</h3>
      {error && <p className="error">{error}</p>}
      <div className="inline-form">
        <input placeholder="email@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
        <select value={roleId} onChange={(e) => setRoleId(e.target.value)}>
          <option value="">No role</option>
          {roles.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
        </select>
        <button onClick={invite}>Invite</button>
      </div>

      <table>
        <thead><tr><th>Email</th><th>Role</th><th>Created</th><th></th></tr></thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>{u.email}</td>
              <td>{roles.find((r) => r.id === u.role_id)?.name ?? '—'}</td>
              <td>{new Date(u.created_at).toLocaleDateString()}</td>
              <td><button className="btn-sm btn-danger" onClick={() => deactivate(u.id)}>Deactivate</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
