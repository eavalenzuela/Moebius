import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { PaginatedResponse, Role } from '../../types/api';

const ALL_PERMISSIONS = [
  'devices:read', 'devices:write', 'devices:revoke',
  'jobs:read', 'jobs:create', 'jobs:retry',
  'packages:deploy', 'inventory:read', 'inventory:request',
  'groups:read', 'groups:write', 'tags:read', 'tags:write',
  'sites:read', 'sites:write',
  'users:read', 'users:write', 'roles:read', 'roles:write',
  'api_keys:read', 'api_keys:write', 'enrollment_tokens:write',
  'alerts:read', 'alerts:write', 'audit_log:read',
  'tenant:read', 'tenant:write',
  'scheduled_jobs:read', 'scheduled_jobs:write',
];

export default function Roles() {
  const { data, refetch } = useFetch<PaginatedResponse<Role>>('/roles?limit=200');
  const [showCreate, setShowCreate] = useState(false);
  const roles = data?.data ?? [];

  async function remove(id: string) {
    await api.del(`/roles/${id}`);
    refetch();
  }

  return (
    <div>
      <div className="page-header">
        <h3>Roles</h3>
        <button className="btn btn-primary" onClick={() => setShowCreate(!showCreate)}>
          {showCreate ? 'Cancel' : 'Create Role'}
        </button>
      </div>

      {showCreate && <CreateRole onDone={() => { setShowCreate(false); refetch(); }} />}

      <table>
        <thead><tr><th>Name</th><th>Custom</th><th>Permissions</th><th></th></tr></thead>
        <tbody>
          {roles.map((r) => (
            <tr key={r.id}>
              <td>{r.name}</td>
              <td>{r.is_custom ? 'Yes' : 'System'}</td>
              <td>{(r.permissions ?? []).length} permission(s)</td>
              <td>
                {r.is_custom && <button className="btn-sm btn-danger" onClick={() => remove(r.id)}>Delete</button>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CreateRole({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('');
  const [perms, setPerms] = useState<Set<string>>(new Set());
  const [error, setError] = useState('');

  function togglePerm(p: string) {
    setPerms((prev) => {
      const next = new Set(prev);
      if (next.has(p)) next.delete(p); else next.add(p);
      return next;
    });
  }

  async function submit() {
    setError('');
    try {
      await api.post('/roles', { name, permissions: [...perms] });
      onDone();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed');
    }
  }

  return (
    <div className="form card">
      {error && <p className="error">{error}</p>}
      <label>Role Name <input value={name} onChange={(e) => setName(e.target.value)} required /></label>
      <fieldset>
        <legend>Permissions</legend>
        <div className="perm-grid">
          {ALL_PERMISSIONS.map((p) => (
            <label key={p} className="checkbox">
              <input type="checkbox" checked={perms.has(p)} onChange={() => togglePerm(p)} />
              {p}
            </label>
          ))}
        </div>
      </fieldset>
      <button className="btn btn-primary" onClick={submit}>Create Role</button>
    </div>
  );
}
