import { FormEvent, useCallback, useEffect, useState } from 'react';
import { api, Role, User } from '../api/client';
import { useAuth } from '../auth';

function formatDate(iso?: string): string {
  return iso ? new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' }) : '—';
}

function CreateUser({ onCreated }: { onCreated: () => void }) {
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
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="form create-user">
      <div className="form-grid">
        <label>
          Email
          <input type="email" required autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label>
          Initial password
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
          Role
          <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="user">User</option>
            <option value="admin">Admin</option>
          </select>
        </label>
      </div>
      {error && <p className="error">{error}</p>}
      <button type="submit" disabled={busy}>
        {busy ? 'Creating…' : 'Create user'}
      </button>
    </form>
  );
}

type Mode = null | 'edit' | 'password';

function UserRow({ u, self, onChanged }: { u: User; self: boolean; onChanged: () => void }) {
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
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  function handleDelete() {
    if (
      window.confirm(
        `Delete ${u.email}? Their devices, conversations and audio are deleted permanently, and they can no longer sign in.`,
      )
    )
      run(() => api.deleteUser(u.id), 'Deleted.');
  }

  return (
    <li className="user-row">
      <div className="user-head">
        <div className="user-main">
          <span className="user-email">{u.email}</span>
          <span className="user-badges">
            <span className={`role-pill${u.role === 'admin' ? '' : ' plain'}`}>{u.role === 'admin' ? 'Admin' : 'User'}</span>
            {u.builtIn && <span className="role-pill plain">Built-in</span>}
            {self && <span className="role-pill plain">You</span>}
            {u.pocketConfigured && <span className="role-pill plain">Pocket</span>}
          </span>
          <span className="muted user-meta">
            Created {formatDate(u.createdAt)}
            {u.usesEnvPassword ? ' · password from ADMIN_PASSWORD' : u.passwordChangedAt ? ` · password set ${formatDate(u.passwordChangedAt)}` : ''}
          </span>
        </div>
        <div className="user-actions">
          <button type="button" className="small-button" disabled={busy} onClick={() => setMode(mode === 'edit' ? null : 'edit')}>
            Edit
          </button>
          <button type="button" className="small-button" disabled={busy} onClick={() => setMode(mode === 'password' ? null : 'password')}>
            Set password
          </button>
          {!u.builtIn && !self && (
            <button type="button" className="small-button danger" disabled={busy} onClick={handleDelete}>
              Delete
            </button>
          )}
        </div>
      </div>

      {mode === 'edit' && (
        <form
          className="form inline-edit"
          onSubmit={(e) => {
            e.preventDefault();
            run(() => api.updateUser(u.id, { email, role }), 'Saved.');
          }}
        >
          <div className="form-grid">
            <label>
              Email
              <input type="email" required value={email} disabled={u.builtIn} onChange={(e) => setEmail(e.target.value)} />
            </label>
            <label>
              Role
              <select value={role} disabled={u.builtIn} onChange={(e) => setRole(e.target.value as Role)}>
                <option value="user">User</option>
                <option value="admin">Admin</option>
              </select>
            </label>
          </div>
          {u.builtIn && <p className="muted field-note">The built-in admin's email comes from ADMIN_EMAIL and its role can't change.</p>}
          <div className="button-row">
            <button type="submit" disabled={busy || u.builtIn}>
              Save
            </button>
            <button type="button" className="secondary-button" onClick={() => setMode(null)}>
              Cancel
            </button>
          </div>
        </form>
      )}

      {mode === 'password' && (
        <form
          className="form inline-edit"
          onSubmit={(e) => {
            e.preventDefault();
            run(() => api.setUserPassword(u.id, password), 'Password set. The user was signed out everywhere.');
          }}
        >
          <label>
            New password for {u.email}
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
              Set password
            </button>
            <button type="button" className="secondary-button" onClick={() => setMode(null)}>
              Cancel
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
  const { account } = useAuth();
  const [users, setUsers] = useState<User[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.users().then(setUsers, (e: Error) => setError(e.message));
  }, []);
  useEffect(load, [load]);

  return (
    <div className="page">
      <section className="card">
        <h1>Users</h1>
        <p className="muted">
          Everyone signs in with email and password and sees only their own devices and conversations. Admins also manage
          users and the AI settings.
        </p>
        <h2 className="card-title">Add a user</h2>
        <CreateUser onCreated={load} />
      </section>
      <section className="card">
        <h2 className="card-title">All users</h2>
        {error && <p className="error">{error}</p>}
        {!users && !error && <p className="muted">Loading…</p>}
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
