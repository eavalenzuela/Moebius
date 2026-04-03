import { useState, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api/client';
import StatusBadge from '../../components/StatusBadge';
import TimeAgo from '../../components/TimeAgo';
import Pagination from '../../components/Pagination';
import type { PaginatedResponse, Device } from '../../types/api';

export default function DeviceList() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [cursor, setCursor] = useState('');
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [osFilter, setOsFilter] = useState('');
  const [initialLoad, setInitialLoad] = useState(true);

  const fetchDevices = useCallback(async (c: string = '') => {
    setLoading(true);
    try {
      const params = new URLSearchParams({ limit: '50' });
      if (c) params.set('cursor', c);
      if (search) params.set('search', search);
      if (statusFilter) params.set('status', statusFilter);
      if (osFilter) params.set('os', osFilter);
      const res = await api.get<PaginatedResponse<Device>>(`/devices?${params}`);
      setDevices(res.data);
      setCursor(res.pagination.next_cursor);
      setHasMore(res.pagination.has_more);
      setInitialLoad(false);
    } catch {
      // errors handled by global handler
    } finally {
      setLoading(false);
    }
  }, [search, statusFilter, osFilter]);

  // Load on first render
  if (initialLoad && !loading) fetchDevices();

  return (
    <div>
      <div className="page-header">
        <h2>Devices</h2>
        <Link to="/devices/add" className="btn btn-primary">Add Device</Link>
      </div>

      <div className="filters">
        <input
          placeholder="Search hostname..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && fetchDevices()}
        />
        <select value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); }}>
          <option value="">All statuses</option>
          <option value="online">Online</option>
          <option value="offline">Offline</option>
        </select>
        <select value={osFilter} onChange={(e) => { setOsFilter(e.target.value); }}>
          <option value="">All OS</option>
          <option value="linux">Linux</option>
          <option value="windows">Windows</option>
        </select>
        <button onClick={() => fetchDevices()}>Search</button>
      </div>

      <table>
        <thead>
          <tr>
            <th>Hostname</th>
            <th>OS</th>
            <th>Arch</th>
            <th>Agent</th>
            <th>Status</th>
            <th>CDM</th>
            <th>Last Seen</th>
          </tr>
        </thead>
        <tbody>
          {devices.map((d) => (
            <tr key={d.id}>
              <td><Link to={`/devices/${d.id}`}>{d.hostname}</Link></td>
              <td>{d.os} {d.os_version}</td>
              <td>{d.arch}</td>
              <td>{d.agent_version}</td>
              <td><StatusBadge status={d.status} /></td>
              <td>{d.cdm_enabled ? (d.cdm_session_active ? 'Session Active' : 'Enabled') : 'Off'}</td>
              <td><TimeAgo ts={d.last_seen_at} /></td>
            </tr>
          ))}
          {!loading && devices.length === 0 && (
            <tr><td colSpan={7} className="empty">No devices found.</td></tr>
          )}
        </tbody>
      </table>

      <Pagination hasMore={hasMore} onNext={() => fetchDevices(cursor)} onReset={() => fetchDevices()} loading={loading} />
    </div>
  );
}
