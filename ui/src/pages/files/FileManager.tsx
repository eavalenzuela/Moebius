import { useState, useRef } from 'react';
import { useFetch } from '../../hooks/useApi';
import TimeAgo from '../../components/TimeAgo';
import type { PaginatedResponse, FileRecord } from '../../types/api';

const CHUNK_SIZE = 4 * 1024 * 1024; // 4 MB

export default function FileManager() {
  const { data, loading, refetch } = useFetch<PaginatedResponse<FileRecord>>('/files?limit=50');
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const files = data?.data ?? [];

  async function handleUpload() {
    const file = fileRef.current?.files?.[0];
    if (!file) return;

    setUploading(true);
    setProgress(0);
    setError('');

    try {
      const token = localStorage.getItem('api_key');
      const headers: Record<string, string> = {};
      if (token) headers['Authorization'] = `Bearer ${token}`;

      // Initiate upload
      const initRes = await fetch('/v1/files/upload', {
        method: 'POST',
        headers: { ...headers, 'Content-Type': 'application/json' },
        body: JSON.stringify({
          filename: file.name,
          size_bytes: file.size,
          chunk_size_bytes: CHUNK_SIZE,
        }),
      });

      if (!initRes.ok) throw new Error('Failed to initiate upload');
      const { upload_id, total_chunks } = await initRes.json();

      // Upload chunks
      for (let i = 0; i < total_chunks; i++) {
        const start = i * CHUNK_SIZE;
        const end = Math.min(start + CHUNK_SIZE, file.size);
        const chunk = file.slice(start, end);

        const formData = new FormData();
        formData.append('chunk', chunk);

        const chunkRes = await fetch(`/v1/files/upload/${upload_id}/chunks/${i}`, {
          method: 'PUT',
          headers,
          body: formData,
        });

        if (!chunkRes.ok) throw new Error(`Failed to upload chunk ${i}`);
        setProgress(Math.round(((i + 1) / total_chunks) * 100));
      }

      // Complete upload
      const completeRes = await fetch(`/v1/files/upload/${upload_id}/complete`, {
        method: 'POST',
        headers,
      });
      if (!completeRes.ok) throw new Error('Failed to complete upload');

      refetch();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Upload failed');
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  }

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  }

  return (
    <div>
      <h2>Files</h2>

      <div className="card">
        <h3>Upload File</h3>
        <div className="inline-form">
          <input type="file" ref={fileRef} disabled={uploading} />
          <button className="btn btn-primary" onClick={handleUpload} disabled={uploading}>
            {uploading ? `Uploading... ${progress}%` : 'Upload'}
          </button>
        </div>
        {uploading && (
          <div className="progress-bar">
            <div className="progress-fill" style={{ width: `${progress}%` }} />
          </div>
        )}
        {error && <p className="error">{error}</p>}
      </div>

      {loading ? <p>Loading...</p> : (
        <table>
          <thead>
            <tr><th>Filename</th><th>Size</th><th>SHA-256</th><th>Uploaded</th></tr>
          </thead>
          <tbody>
            {files.map((f) => (
              <tr key={f.id}>
                <td>{f.filename}</td>
                <td>{formatSize(f.size_bytes)}</td>
                <td className="muted" title={f.sha256}>{f.sha256.slice(0, 16)}...</td>
                <td><TimeAgo ts={f.created_at} /></td>
              </tr>
            ))}
            {files.length === 0 && <tr><td colSpan={4} className="empty">No files.</td></tr>}
          </tbody>
        </table>
      )}
    </div>
  );
}
