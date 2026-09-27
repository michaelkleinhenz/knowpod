import { FormEvent, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { api } from '../api/client';
import { useAuth } from '../auth';
import { NotificationSettings } from '../components/NotificationSettings';
import { PocketSetup } from '../components/PocketSetup';
import { RemarkableSetup } from '../components/RemarkableSetup';
import { errorText } from '../lib/errors';

function ChangePassword() {
  const { t } = useTranslation();
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
      setError(t('account.mismatch'));
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
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="form">
      <label>
        {t('account.current')}
        <input type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      </label>
      <label>
        {t('account.new')}
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
        {t('account.repeat')}
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
      {done && <p className="success">{t('account.changed')}</p>}
      <button type="submit" disabled={busy}>
        {busy ? t('common.saving') : t('account.change')}
      </button>
    </form>
  );
}

export function Account() {
  const { t } = useTranslation();
  const { account } = useAuth();
  return (
    <div className="page">
      <section className="card">
        <h1>{t('account.title')}</h1>
        <p className="muted">
          <Trans i18nKey="account.signedInAs" values={{ email: account?.email }} components={{ 1: <strong /> }} />
          {account?.role === 'admin' && <span className="role-pill">{t('common.admin')}</span>}
        </p>
        <h2 className="card-title">{t('account.changePassword')}</h2>
        <ChangePassword />
      </section>
      <section className="card" id="notifications">
        <h2 className="card-title">{t('notifications.title')}</h2>
        <NotificationSettings />
      </section>
      <section className="card">
        <h2 className="card-title">{t('account.pocketTitle')}</h2>
        <PocketSetup />
      </section>
      <section className="card">
        <h2 className="card-title">{t('account.remarkableTitle')}</h2>
        <RemarkableSetup />
      </section>
    </div>
  );
}
