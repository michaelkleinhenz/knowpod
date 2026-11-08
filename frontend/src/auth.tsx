import { createContext, FormEvent, ReactNode, useCallback, useContext, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Account, api, ApiError, SIGNED_IN_EVENT, UNAUTHORIZED_EVENT } from './api/client';
import { clearOffline, readOffline, writeOffline } from './api/offline';
import i18n, { applyLanguage } from './i18n';
import { applyAppearance } from './lib/appearance';
import { isApp } from './lib/desktop';
import { clearDrafts } from './lib/drafts';
import { errorText } from './lib/errors';
import { applyFontSize } from './lib/fontSize';

interface AuthState {
  account: Account | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  // logoutAll signs out on all devices, this one included.
  logoutAll: () => Promise<void>;
  // update replaces the signed-in account (e.g. after changing preferences).
  update: (a: Account) => void;
}

const AuthContext = createContext<AuthState | null>(null);

// AuthProvider asks the backend who is signed in (the session cookie itself is not readable
// by scripts) and exposes login/logout to the app. Offline, the account kept from the last
// visit stays signed in, so kept notes can be read. Signing out, or in as someone else,
// drops everything kept offline.
//
// The desktop and mobile apps sign in once: their session never expires, and ends only when
// the user signs out or signs out on all devices. When a session does end while the app is
// open (signed out on all devices elsewhere, password changed), nothing is navigated away:
// a dialog asks for the password over the page, so unsaved edits stay, and pending saves are
// retried once signed in again (SIGNED_IN_EVENT; edits are also kept as drafts, lib/drafts.ts).
export function AuthProvider({ children }: { children: ReactNode }) {
  const [account, setAccount] = useState<Account | null>(null);
  const [loading, setLoading] = useState(true);
  // The session ended while signed in: the sign-in dialog is shown over the page.
  const [expired, setExpired] = useState(false);

  useEffect(() => {
    api
      .me()
      .then((a) => {
        applyLanguage(a.language);
        applyAppearance(a.appearance);
        applyFontSize(a.fontSize);
        setAccount(a);
      })
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) void clearOffline();
        else console.error(e);
        setAccount(null);
      })
      .finally(() => setLoading(false));
  }, []);

  const signedIn = !!account;
  useEffect(() => {
    if (!signedIn) return;
    const onUnauthorized = () => setExpired(true);
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [signedIn]);

  const login = useCallback(async (email: string, password: string) => {
    const a = await api.login(email, password, isApp());
    const before = await readOffline<Account>('/auth/me');
    if (before && before.id !== a.id) {
      await clearOffline();
      clearDrafts(a.id);
    }
    void writeOffline('/auth/me', a);
    applyLanguage(a.language);
    applyAppearance(a.appearance);
    applyFontSize(a.fontSize);
    setAccount(a);
    setExpired(false);
    window.dispatchEvent(new Event(SIGNED_IN_EVENT));
  }, []);

  const logout = useCallback(async () => {
    await api.logout();
    await clearOffline();
    setExpired(false);
    setAccount(null);
  }, []);

  const logoutAll = useCallback(async () => {
    await api.logoutAll();
    await clearOffline();
    setExpired(false);
    setAccount(null);
  }, []);

  const update = useCallback((a: Account) => {
    void writeOffline('/auth/me', a);
    applyLanguage(a.language);
    applyAppearance(a.appearance);
    applyFontSize(a.fontSize);
    setAccount(a);
  }, []);

  // Task dates and reminders are meant in the time zone of the browser the user works in.
  const zone = account?.timeZone;
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

  // Without a saved language the app shows the browser's, but the server (which writes
  // transcripts and summaries) can't see it: save the one shown, so both agree.
  const language = account?.language;
  useEffect(() => {
    if (!signedIn || language) return;
    api
      .savePreferences({ language: i18n.language === 'de' ? 'de' : 'en' })
      .then(update)
      .catch(() => undefined); // offline: tried again next time
  }, [signedIn, language, update]);

  return (
    <AuthContext.Provider value={{ account, loading, login, logout, logoutAll, update }}>
      {children}
      {expired && account && <SignInAgain email={account.email} login={login} logout={logout} />}
    </AuthContext.Provider>
  );
}

// SignInAgain asks for the password when the session ended while the app was open. It is
// shown over the page, which stays as it is, unsaved edits included.
function SignInAgain({ email, login, logout }: { email: string; login: AuthState['login']; logout: AuthState['logout'] }) {
  const { t } = useTranslation();
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(email, password);
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  return (
    <div className="new-item-backdrop">
      <form className="new-item-dialog" role="dialog" aria-modal="true" aria-label={t('login.again')} onSubmit={submit}>
        <h2>{t('login.again')}</h2>
        <p className="muted">{t('login.againHint', { email })}</p>
        <input type="email" autoComplete="username" value={email} readOnly hidden />
        <label className="form">
          {t('login.password')}
          <input type="password" autoComplete="current-password" required autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        {error && <p className="error">{error}</p>}
        <div className="new-item-actions">
          <button type="button" className="pill-button" onClick={() => void logout().catch(() => undefined).finally(() => window.location.assign('/login'))}>
            {t('login.switch')}
          </button>
          <button type="submit" disabled={busy}>
            {busy ? t('login.submitting') : t('login.submit')}
          </button>
        </div>
      </form>
    </div>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}
