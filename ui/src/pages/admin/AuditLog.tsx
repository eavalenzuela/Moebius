import { useState, useCallback } from 'react';
import { api } from '../../api/client';
import Pagination from '../../components/Pagination';
import TimeAgo from '../../components/TimeAgo';
import type { PaginatedResponse, AuditEntry } from '../../types/api';

export default function AuditLog() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [cursor, setCursor] = useState('');
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [actionFilter, setActionFilter] = useState('');
  const [resourceFilter, setResourceFilter] = useState('');
  const [initialLoad, setInitialLoad] = useState(true);

  const fetchEntries = useCallback(async (c: string = '') => {
    setLoading(true);
    try {
      const params = new URLSearchParams({ limit: '100' });
      if (c) params.set('cursor', c);
      if (actionFilter) params.set('action', actionFilter);
      if (resourceFilter) params.set('resource_type', resourceFilter);
      const res = await api.get<PaginatedResponse<AuditEntry>>(`/audit-log?${params}`);
      setEntries(res.data);
      setCursor(res.pagination.next_cursor);
      setHasMore(res.pagination.has_more);
      setInitialLoad(false);
    } finally {
      setLoading(false);
    }
  }, [actionFilter, resourceFilter]);

  if (initialLoad && !loading) fetchEntries();

  return (
    <div>
      <h3>Audit Log</h3>

      <div className="filters">
        <input placeholder="Filter by action..." value={actionFilter} onChange={(e) => setActionFilter(e.target.value)} />
        <input placeholder="Filter by resource type..." value={resourceFilter} onChange={(e) => setResourceFilter(e.target.value)} />
        <button onClick={() => fetchEntries()}>Filter</button>
      </div>

      <table>
        <thead>
          <tr><th>Time</th><th>Actor</th><th>Action</th><th>Resource</th><th>IP</th></tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <tr key={e.id}>
              <td><TimeAgo ts={e.created_at} /></td>
              <td>{e.actor_type}: {e.actor_id.slice(0, 12)}...</td>
              <td>{e.action}</td>
              <td>{e.resource_type}{e.resource_id ? `: ${e.resource_id.slice(0, 12)}...` : ''}</td>
              <td className="muted">{e.ip_address ?? '—'}</td>
            </tr>
          ))}
          {!loading && entries.length === 0 && <tr><td colSpan={5} className="empty">No audit entries.</td></tr>}
        </tbody>
      </table>

      <Pagination hasMore={hasMore} onNext={() => fetchEntries(cursor)} onReset={() => fetchEntries()} loading={loading} />
    </div>
  );
}
