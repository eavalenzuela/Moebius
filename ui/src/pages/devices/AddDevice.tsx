import { useState } from 'react';
import { api } from '../../api/client';
import type { EnrollmentToken } from '../../types/api';

export default function AddDevice() {
  const [step, setStep] = useState<'os' | 'token' | 'command'>('os');
  const [os, setOs] = useState('linux');
  const [arch, setArch] = useState('amd64');
  const [tokenId, setTokenId] = useState('');
  const [command, setCommand] = useState('');
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);

  async function generateToken() {
    setError('');
    try {
      const res = await api.post<EnrollmentToken>('/enrollment-tokens', {
        expires_in_seconds: 86400, // 24 hours
      });
      setTokenId(res.id);
      await generateCommand(res.id);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to create token');
    }
  }

  async function generateCommand(tid: string) {
    try {
      const res = await api.post<{ command: string }>(`/enrollment-tokens/${tid}/install-command`, { os, arch });
      setCommand(res.command);
      setStep('command');
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to generate command');
    }
  }

  function copyCommand() {
    navigator.clipboard.writeText(command);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  }

  return (
    <div>
      <h2>Add Device</h2>

      {error && <p className="error">{error}</p>}

      {step === 'os' && (
        <div className="form card">
          <p>Select the target device's operating system and architecture.</p>
          <label>
            Operating System
            <select value={os} onChange={(e) => setOs(e.target.value)}>
              <option value="linux">Linux</option>
              <option value="windows">Windows</option>
            </select>
          </label>
          <label>
            Architecture
            <select value={arch} onChange={(e) => setArch(e.target.value)}>
              <option value="amd64">x86_64 (amd64)</option>
              <option value="arm64">ARM64 (aarch64)</option>
            </select>
          </label>
          <button className="btn btn-primary" onClick={() => { setStep('token'); generateToken(); }}>
            Generate Install Command
          </button>
        </div>
      )}

      {step === 'token' && !command && (
        <p>Generating enrollment token and install command...</p>
      )}

      {step === 'command' && command && (
        <div className="card">
          <h3>Install Command</h3>
          <p>Run this command on the target {os}/{arch} device. The token expires in 24 hours.</p>
          <div className="command-block">
            <pre>{command}</pre>
            <button className="btn btn-primary" onClick={copyCommand}>
              {copied ? 'Copied!' : 'Copy'}
            </button>
          </div>
          <p className="muted">Token ID: {tokenId}</p>
          <button className="btn" onClick={() => { setStep('os'); setCommand(''); setTokenId(''); }}>
            Generate Another
          </button>
        </div>
      )}
    </div>
  );
}
