import { useState, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api/client';
import StatusBadge from '../../components/StatusBadge';
import TimeAgo from '../../components/TimeAgo';
import Pagination from '../../components/Pagination';
import type { PaginatedResponse, Job } from '../../types/api';

export default function JobList() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [cursor, setCursor] = useState('');
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [statusFilter, setStatusFilter] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [initialLoad, setInitialLoad] = useState(true);

  const fetchJobs = useCallback(async (c: string = '') => {
    setLoading(true);
    try {
      const params = new URLSearchParams({ limit: '50' });
      if (c) params.set('cursor', c);
      if (statusFilter) params.set('status', statusFilter);
      if (typeFilter) params.set('type', typeFilter);
      const res = await api.get<PaginatedResponse<Job>>(`/jobs?${params}`);
      setJobs(res.data);
      setCursor(res.pagination.next_cursor);
      setHasMore(res.pagination.has_more);
      setInitialLoad(false);
    } finally {
      setLoading(false);
    }
  }, [statusFilter, typeFilter]);

  if (initialLoad && !loading) fetchJobs();

  return (
    <div>
      <div className="page-header">
        <h2>Jobs</h2>
        <Link to="/jobs/new" className="btn btn-primary">Create Job</Link>
      </div>

      <div className="filters">
        <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="">All statuses</option>
          {['queued', 'dispatched', 'acknowledged', 'running', 'completed', 'failed', 'timed_out', 'cancelled', 'cdm_hold'].map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
        <select value={typeFilter} onChange={(e) => setTypeFilter(e.target.value)}>
          <option value="">All types</option>
          {['exec', 'package_install', 'package_remove', 'package_update', 'file_transfer', 'inventory_full', 'agent_update'].map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <button onClick={() => fetchJobs()}>Filter</button>
      </div>

      <table>
        <thead>
          <tr><th>ID</th><th>Device</th><th>Type</th><th>Status</th><th>Created</th><th>Completed</th></tr>
        </thead>
        <tbody>
          {jobs.map((j) => (
            <tr key={j.id}>
              <td><Link to={`/jobs/${j.id}`}>{j.id.slice(0, 12)}...</Link></td>
              <td><Link to={`/devices/${j.device_id}`}>{j.device_id.slice(0, 12)}...</Link></td>
              <td>{j.type}</td>
              <td><StatusBadge status={j.status} /></td>
              <td><TimeAgo ts={j.created_at} /></td>
              <td><TimeAgo ts={j.completed_at} /></td>
            </tr>
          ))}
          {!loading && jobs.length === 0 && <tr><td colSpan={6} className="empty">No jobs found.</td></tr>}
        </tbody>
      </table>

      <Pagination hasMore={hasMore} onNext={() => fetchJobs(cursor)} onReset={() => fetchJobs()} loading={loading} />
    </div>
  );
}
