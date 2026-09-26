import { FormEvent, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Role, User } from '../api/client';
import { useAuth } from '../auth';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';

function CreateUser({ onCreated }: { onCreated: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('user');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.createUser({ email, password, role });
      setEmail('');
      setPassword('');
      setRole('user');
      onCreated();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="form create-user">
      <div className="form-grid">
        <label>
          {t('users.email')}
          <input type="email" required autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label>
          {t('users.initialPassword')}
          <input
            type="password"
            required
            minLength={8}
            maxLength={72}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label>
          {t('users.role')}
          <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="user">{t('common.user')}</option>
            <option value="admin">{t('common.admin')}</option>
          </select>
        </label>
      </div>
      {error && <p className="error">{error}</p>}
      <button type="submit" disabled={busy}>
        {busy ? t('users.creating') : t('users.create')}
      </button>
    </form>
  );
}

type Mode = null | 'edit' | 'password';

function UserRow({ u, self, onChanged }: { u: User; self: boolean; onChanged: () => void }) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>(null);
  const [email, setEmail] = useState(u.email);
  const [role, setRole] = useState<Role>(u.role);
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function run(action: () => Promise<unknown>, success: string) {
    setBusy(true);
    setError(null);
    setInfo(null);
    try {
      await action();
      setMode(null);
      setPassword('');
      setInfo(success);
      onChanged();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  function handleDelete() {
    if (
      window.confirm(
        t('users.deleteConfirm', { email: u.email }),
      )
    )
      run(() => api.deleteUser(u.id), t('users.deleted'));
  }

  return (
    <li className="user-row">
      <div className="user-head">
        <div className="user-main">
          <span className="user-email">{u.email}</span>
          <span className="user-badges">
            <span className={`role-pill${u.role === 'admin' ? '' : ' plain'}`}>{u.role === 'admin' ? t('common.admin') : t('common.user')}</span>
            {u.builtIn && <span className="role-pill plain">{t('common.builtIn')}</span>}
            {self && <span className="role-pill plain">{t('common.you')}</span>}
            {u.pocketConfigured && <span className="role-pill plain">{t('users.pocket')}</span>}
          </span>
          <span className="muted user-meta">
            {t('users.created', { date: formatDate(u.createdAt) })}
            {u.usesEnvPassword
              ? ` · ${t('users.passwordFromEnv')}`
              : u.passwordChangedAt
                ? ` · ${t('users.passwordSetOn', { date: formatDate(u.passwordChangedAt) })}`
                : ''}
          </span>
        </div>
        <div className="user-actions">
          <button type="button" className="small-button" disabled={busy} onClick={() => setMode(mode === 'edit' ? null : 'edit')}>
            {t('common.edit')}
          </button>
          <button type="button" className="small-button" disabled={busy} onClick={() => setMode(mode === 'password' ? null : 'password')}>
            {t('users.setPassword')}
          </button>
          {!u.builtIn && !self && (
            <button type="button" className="small-button danger" disabled={busy} onClick={handleDelete}>
              {t('common.delete')}
            </button>
          )}
        </div>
      </div>

      {mode === 'edit' && (
        <form
          className="form inline-edit"
          onSubmit={(e) => {
            e.preventDefault();
            run(() => api.updateUser(u.id, { email, role }), t('common.saved'));
          }}
        >
          <div className="form-grid">
            <label>
              {t('users.email')}
              <input type="email" required value={email} disabled={u.builtIn} onChange={(e) => setEmail(e.target.value)} />
            </label>
            <label>
              {t('users.role')}
              <select value={role} disabled={u.builtIn} onChange={(e) => setRole(e.target.value as Role)}>
                <option value="user">{t('common.user')}</option>
                <option value="admin">{t('common.admin')}</option>
              </select>
            </label>
          </div>
          {u.builtIn && <p className="muted field-note">{t('users.builtInNote')}</p>}
          <div className="button-row">
            <button type="submit" disabled={busy || u.builtIn}>
              {t('common.save')}
            </button>
            <button type="button" className="secondary-button" onClick={() => setMode(null)}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      )}

      {mode === 'password' && (
        <form
          className="form inline-edit"
          onSubmit={(e) => {
            e.preventDefault();
            run(() => api.setUserPassword(u.id, password), t('users.passwordSet'));
          }}
        >
          <label>
            {t('users.newPasswordFor', { email: u.email })}
            <input
              type="password"
              required
              minLength={8}
              maxLength={72}
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <div className="button-row">
            <button type="submit" disabled={busy}>
              {t('users.setPassword')}
            </button>
            <button type="button" className="secondary-button" onClick={() => setMode(null)}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      )}
      {error && <p className="error">{error}</p>}
      {info && <p className="success">{info}</p>}
    </li>
  );
}

export function Users() {
  const { t } = useTranslation();
  const { account } = useAuth();
  const [users, setUsers] = useState<User[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.users().then(setUsers, (e) => setError(errorText(e, t)));
  }, [t]);
  useEffect(load, [load]);

  return (
    <div className="page">
      <section className="card">
        <h1>{t('users.title')}</h1>
        <p className="muted">{t('users.intro')}</p>
        <h2 className="card-title">{t('users.add')}</h2>
        <CreateUser onCreated={load} />
      </section>
      <section className="card">
        <h2 className="card-title">{t('users.all')}</h2>
        {error && <p className="error">{error}</p>}
        {!users && !error && <p className="muted">{t('common.loading')}</p>}
        {users && (
          <ul className="user-list">
            {users.map((u) => (
              <UserRow key={u.id} u={u} self={u.id === account?.id} onChanged={load} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
