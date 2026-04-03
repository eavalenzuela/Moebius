import { useState } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { PaginatedResponse, Group, Tag, Site } from '../../types/api';

type Section = 'groups' | 'tags' | 'sites';

export default function GroupsTagsSites() {
  const [section, setSection] = useState<Section>('groups');

  return (
    <div>
      <h2>Groups, Tags &amp; Sites</h2>
      <div className="tabs">
        {(['groups', 'tags', 'sites'] as Section[]).map((s) => (
          <button key={s} className={section === s ? 'tab active' : 'tab'} onClick={() => setSection(s)}>
            {s.charAt(0).toUpperCase() + s.slice(1)}
          </button>
        ))}
      </div>

      {section === 'groups' && <GroupPanel />}
      {section === 'tags' && <TagPanel />}
      {section === 'sites' && <SitePanel />}
    </div>
  );
}

function GroupPanel() {
  const { data, refetch } = useFetch<PaginatedResponse<Group>>('/groups?limit=200');
  const [name, setName] = useState('');

  async function create() {
    if (!name.trim()) return;
    await api.post('/groups', { name: name.trim() });
    setName('');
    refetch();
  }

  async function remove(id: string) {
    await api.del(`/groups/${id}`);
    refetch();
  }

  const items = data?.data ?? [];
  return (
    <div>
      <div className="inline-form">
        <input placeholder="New group name" value={name} onChange={(e) => setName(e.target.value)} />
        <button onClick={create}>Create</button>
      </div>
      <table>
        <thead><tr><th>Name</th><th>ID</th><th></th></tr></thead>
        <tbody>
          {items.map((g) => (
            <tr key={g.id}>
              <td>{g.name}</td>
              <td className="muted">{g.id}</td>
              <td><button className="btn-sm btn-danger" onClick={() => remove(g.id)}>Delete</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TagPanel() {
  const { data, refetch } = useFetch<PaginatedResponse<Tag>>('/tags?limit=200');
  const [name, setName] = useState('');

  async function create() {
    if (!name.trim()) return;
    await api.post('/tags', { name: name.trim() });
    setName('');
    refetch();
  }

  async function remove(id: string) {
    await api.del(`/tags/${id}`);
    refetch();
  }

  const items = data?.data ?? [];
  return (
    <div>
      <div className="inline-form">
        <input placeholder="New tag name" value={name} onChange={(e) => setName(e.target.value)} />
        <button onClick={create}>Create</button>
      </div>
      <table>
        <thead><tr><th>Name</th><th>ID</th><th></th></tr></thead>
        <tbody>
          {items.map((t) => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td className="muted">{t.id}</td>
              <td><button className="btn-sm btn-danger" onClick={() => remove(t.id)}>Delete</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SitePanel() {
  const { data, refetch } = useFetch<PaginatedResponse<Site>>('/sites?limit=200');
  const [name, setName] = useState('');
  const [location, setLocation] = useState('');

  async function create() {
    if (!name.trim()) return;
    await api.post('/sites', { name: name.trim(), location: location.trim() });
    setName('');
    setLocation('');
    refetch();
  }

  async function remove(id: string) {
    await api.del(`/sites/${id}`);
    refetch();
  }

  const items = data?.data ?? [];
  return (
    <div>
      <div className="inline-form">
        <input placeholder="Site name" value={name} onChange={(e) => setName(e.target.value)} />
        <input placeholder="Location" value={location} onChange={(e) => setLocation(e.target.value)} />
        <button onClick={create}>Create</button>
      </div>
      <table>
        <thead><tr><th>Name</th><th>Location</th><th>ID</th><th></th></tr></thead>
        <tbody>
          {items.map((s) => (
            <tr key={s.id}>
              <td>{s.name}</td>
              <td>{s.location}</td>
              <td className="muted">{s.id}</td>
              <td><button className="btn-sm btn-danger" onClick={() => remove(s.id)}>Delete</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
