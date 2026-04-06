import { useState, type FormEvent } from 'react';
import { useAuth } from './useAuth';

export default function LoginPage() {
  const { login } = useAuth();
  const [key, setKey] = useState('');

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (key.trim()) login(key.trim());
  }

  return (
    <div className="login-page">
      <div className="login-card">
        <h1>Moebius</h1>
        <p>Enter your API key to continue.</p>
        <form onSubmit={handleSubmit}>
          <input
            type="password"
            placeholder="sk_..."
            value={key}
            onChange={(e) => setKey(e.target.value)}
            autoFocus
          />
          <button type="submit" disabled={!key.trim()}>Sign In</button>
        </form>
      </div>
    </div>
  );
}
