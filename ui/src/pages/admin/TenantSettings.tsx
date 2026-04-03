import { useState, useEffect } from 'react';
import { api } from '../../api/client';
import { useFetch } from '../../hooks/useApi';
import type { Tenant } from '../../types/api';

export default function TenantSettings() {
  const { data: tenant, loading, refetch } = useFetch<Tenant>('/tenant');
  const [name, setName] = useState('');
  const [pollInterval, setPollInterval] = useState(30);
  const [certLifetime, setCertLifetime] = useState(90);
  const [ssoEnabled, setSsoEnabled] = useState(false);
  const [ssoIssuer, setSsoIssuer] = useState('');
  const [ssoClientId, setSsoClientId] = useState('');
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');

  useEffect(() => {
    if (!tenant) return;
    setName(tenant.name);
    const cfg = tenant.config ?? {};
    setPollInterval((cfg as Record<string, number>).default_poll_interval_seconds ?? 30);
    setCertLifetime((cfg as Record<string, number>).default_cert_lifetime_days ?? 90);
    const sso = (cfg as Record<string, Record<string, unknown>>).sso;
    if (sso) {
      setSsoEnabled(!!sso.enabled);
      setSsoIssuer((sso.issuer_url as string) ?? '');
      setSsoClientId((sso.client_id as string) ?? '');
    }
  }, [tenant]);

  async function save() {
    setSaving(true);
    setMessage('');
    try {
      await api.patch('/tenant', {
        name,
        config: {
          default_poll_interval_seconds: pollInterval,
          default_cert_lifetime_days: certLifetime,
          sso: {
            enabled: ssoEnabled,
            issuer_url: ssoIssuer,
            client_id: ssoClientId,
          },
        },
      });
      setMessage('Saved.');
      refetch();
    } catch (err: unknown) {
      setMessage(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setSaving(false);
    }
  }

  if (loading) return <p>Loading...</p>;

  return (
    <div>
      <h3>Tenant Settings</h3>
      {message && <p className={message === 'Saved.' ? 'success' : 'error'}>{message}</p>}

      <div className="form">
        <label>Tenant Name <input value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>Default Poll Interval (seconds) <input type="number" value={pollInterval} onChange={(e) => setPollInterval(Number(e.target.value))} min={5} /></label>
        <label>Default Cert Lifetime (days) <input type="number" value={certLifetime} onChange={(e) => setCertLifetime(Number(e.target.value))} min={1} /></label>

        <fieldset>
          <legend>SSO / OIDC</legend>
          <label className="checkbox">
            <input type="checkbox" checked={ssoEnabled} onChange={(e) => setSsoEnabled(e.target.checked)} />
            Enable SSO
          </label>
          {ssoEnabled && (
            <>
              <label>Issuer URL <input value={ssoIssuer} onChange={(e) => setSsoIssuer(e.target.value)} placeholder="https://example.okta.com" /></label>
              <label>Client ID <input value={ssoClientId} onChange={(e) => setSsoClientId(e.target.value)} /></label>
            </>
          )}
        </fieldset>

        <button className="btn btn-primary" onClick={save} disabled={saving}>
          {saving ? 'Saving...' : 'Save Settings'}
        </button>
      </div>
    </div>
  );
}
