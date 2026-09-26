import { FormEvent, useState } from 'react';
import { api } from '../api/client';
import { useAuth } from '../auth';
import { PocketSetup } from '../components/PocketSetup';

function ChangePassword() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setDone(false);
    if (next !== confirm) {
      setError('The new passwords do not match.');
      return;
    }
    setBusy(true);
    try {
      await api.changePassword(current, next);
      setDone(true);
      setCurrent('');
      setNext('');
      setConfirm('');
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="form">
      <label>
        Current password
        <input type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      </label>
      <label>
        New password
        <input
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          maxLength={72}
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </label>
      <label>
        Repeat new password
        <input
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          maxLength={72}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </label>
      {error && <p className="error">{error}</p>}
      {done && <p className="success">Password changed. Other signed-in browsers were signed out.</p>}
      <button type="submit" disabled={busy}>
        {busy ? 'Saving…' : 'Change password'}
      </button>
    </form>
  );
}

export function Account() {
  const { account } = useAuth();
  return (
    <div className="page">
      <section className="card">
        <h1>Account</h1>
        <p className="muted">
          Signed in as <strong>{account?.email}</strong>
          {account?.role === 'admin' && <span className="role-pill">Admin</span>}
        </p>
        <h2 className="card-title">Change password</h2>
        <ChangePassword />
      </section>
      <section className="card">
        <h2 className="card-title">Pocket integration</h2>
        <PocketSetup />
      </section>
    </div>
  );
}
