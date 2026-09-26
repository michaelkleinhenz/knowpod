import { createContext, ReactNode, useCallback, useContext, useEffect, useState } from 'react';
import { Account, api, ApiError } from './api/client';

interface AuthState {
  account: Account | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

// AuthProvider asks the backend who is signed in (the session cookie itself is not readable
// by scripts) and exposes login/logout to the app.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [account, setAccount] = useState<Account | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .me()
      .then(setAccount)
      .catch((e) => {
        if (!(e instanceof ApiError && e.status === 401)) console.error(e);
        setAccount(null);
      })
      .finally(() => setLoading(false));
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    setAccount(await api.login(email, password));
  }, []);

  const logout = useCallback(async () => {
    await api.logout();
    setAccount(null);
  }, []);

  return <AuthContext.Provider value={{ account, loading, login, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}
