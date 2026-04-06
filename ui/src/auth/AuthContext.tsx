import { useState, useCallback, type ReactNode } from 'react';
import { AuthContext } from './authContext';

export default function AuthProvider({ children }: { children: ReactNode }) {
  const [apiKey, setApiKey] = useState<string | null>(() => localStorage.getItem('api_key'));

  const login = useCallback((key: string) => {
    localStorage.setItem('api_key', key);
    setApiKey(key);
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem('api_key');
    setApiKey(null);
  }, []);

  return (
    <AuthContext.Provider value={{ apiKey, isAuthenticated: !!apiKey, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}
