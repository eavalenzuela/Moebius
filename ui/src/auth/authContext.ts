import { createContext } from 'react';

export interface AuthState {
  apiKey: string | null;
  isAuthenticated: boolean;
  login: (key: string) => void;
  logout: () => void;
}

export const AuthContext = createContext<AuthState>({
  apiKey: null,
  isAuthenticated: false,
  login: () => {},
  logout: () => {},
});
