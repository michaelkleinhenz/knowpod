import { createContext, ReactNode, useCallback, useContext, useEffect, useState } from 'react';
import { Account, api, ApiError } from './api/client';
import { clearOffline, readOffline, writeOffline } from './api/offline';
import { applyLanguage } from './i18n';
import { applyAppearance } from './lib/appearance';

interface AuthState {
  account: Account | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  // update replaces the signed-in account (e.g. after changing preferences).
  update: (a: Account) => void;
}

const AuthContext = createContext<AuthState | null>(null);

// AuthProvider asks the backend who is signed in (the session cookie itself is not readable
// by scripts) and exposes login/logout to the app. Offline, the account kept from the last
// visit stays signed in, so kept notes can be read. Signing out, or in as someone else,
// drops everything kept offline.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [account, setAccount] = useState<Account | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .me()
      .then((a) => {
        applyLanguage(a.language);
        applyAppearance(a.appearance);
        setAccount(a);
      })
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) void clearOffline();
        else console.error(e);
        setAccount(null);
      })
      .finally(() => setLoading(false));
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    const a = await api.login(email, password);
    const before = await readOffline<Account>('/auth/me');
    if (before && before.id !== a.id) await clearOffline();
    void writeOffline('/auth/me', a);
    applyLanguage(a.language);
    applyAppearance(a.appearance);
    setAccount(a);
  }, []);

  const logout = useCallback(async () => {
    await api.logout();
    await clearOffline();
    setAccount(null);
  }, []);

  const update = useCallback((a: Account) => {
    void writeOffline('/auth/me', a);
    applyLanguage(a.language);
    applyAppearance(a.appearance);
    setAccount(a);
  }, []);

  // Task dates and reminders are meant in the time zone of the browser the user works in.
  const zone = account?.timeZone;
  const signedIn = !!account;
  useEffect(() => {
    if (!signedIn) return;
    let here = '';
    try {
      here = Intl.DateTimeFormat().resolvedOptions().timeZone ?? '';
    } catch {
      return;
    }
    if (!here || here === zone) return;
    api
      .savePreferences({ timeZone: here })
      .then(update)
      .catch(() => undefined); // offline or unknown zone: tried again next time
  }, [signedIn, zone, update]);

  return <AuthContext.Provider value={{ account, loading, login, logout, update }}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}
