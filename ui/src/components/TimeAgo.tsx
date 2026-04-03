export default function TimeAgo({ ts }: { ts: string | null }) {
  if (!ts) return <span className="muted">—</span>;
  const d = new Date(ts);
  const diff = Date.now() - d.getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return <span title={d.toISOString()}>{s}s ago</span>;
  const m = Math.floor(s / 60);
  if (m < 60) return <span title={d.toISOString()}>{m}m ago</span>;
  const h = Math.floor(m / 60);
  if (h < 24) return <span title={d.toISOString()}>{h}h ago</span>;
  return <span title={d.toISOString()}>{d.toLocaleDateString()}</span>;
}
