import { useState, useEffect } from 'react';

function formatAgo(ts: string): string {
  const d = new Date(ts);
  const diff = Date.now() - d.getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return d.toLocaleDateString();
}

export default function TimeAgo({ ts }: { ts: string | null }) {
  const [label, setLabel] = useState(() => (ts ? formatAgo(ts) : ''));

  useEffect(() => {
    if (!ts) return;
    const id = setInterval(() => setLabel(formatAgo(ts)), 30_000);
    return () => clearInterval(id);
  }, [ts]);

  if (!ts) return <span className="muted">&mdash;</span>;
  return <span title={new Date(ts).toISOString()}>{label}</span>;
}
