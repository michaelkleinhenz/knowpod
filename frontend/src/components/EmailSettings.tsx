import { FormEvent, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, EmailSettings as Settings } from '../api/client';
import { errorText } from '../lib/errors';

// EmailSettings is the administrator's setup of outgoing email through Amazon SES, with a
// button that sends a test email.
export function EmailSettings() {
  const { t } = useTranslation();
  const [current, setCurrent] = useState<Settings | null>(null);
  const [region, setRegion] = useState('');
  const [accessKeyId, setAccessKeyId] = useState('');
  const [secret, setSecret] = useState('');
  const [from, setFrom] = useState('');
  const [recipient, setRecipient] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);

  const apply = (s: Settings) => {
    setCurrent(s);
    setRegion(s.region);
    setAccessKeyId(s.accessKeyId);
    setFrom(s.from);
  };

  useEffect(() => {
    api.emailSettings().then(apply, (e) => setError(errorText(e, t)));
  }, []);

  const save = async (update: { secretAccessKey?: string } = {}) => {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      apply(await api.saveEmailSettings({ region, accessKeyId, from, ...update }));
      setSecret('');
      setSaved(true);
    } catch (e) {
      setError(errorText(e, t));
    } finally {
      setBusy(false);
    }
  };

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    void save(secret.trim() ? { secretAccessKey: secret } : {});
  };

  const sendTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      await api.testEmail(recipient);
      setTestResult({ ok: true, text: t('settings.email.testSent', { recipient: recipient.trim() }) });
    } catch (e) {
      setTestResult({ ok: false, text: errorText(e, t) });
    } finally {
      setTesting(false);
    }
  };

  return (
    <>
      <p className="muted">{t('settings.email.intro')}</p>
      <p className="muted">{current?.configured ? t('settings.email.active') : t('settings.email.notConfigured')}</p>
      <form onSubmit={handleSubmit} className="form">
        <label>
          {t('settings.email.region')}
          <input value={region} placeholder="eu-central-1" onChange={(e) => setRegion(e.target.value)} />
        </label>
        <label>
          {t('settings.email.accessKeyId')}
          <input value={accessKeyId} autoComplete="off" onChange={(e) => setAccessKeyId(e.target.value)} />
        </label>
        <label>
          {t('settings.email.secretAccessKey')}
          <input
            type="password"
            autoComplete="off"
            placeholder={current?.secretAccessKeyConfigured ? t('settings.ai.apiKeyPlaceholderSet', { hint: current.secretAccessKeyHint ?? '…' }) : ''}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
          />
        </label>
        {current?.secretAccessKeyConfigured && (
          <p className="field-hint">
            <button
              type="button"
              className="link-button danger-link"
              disabled={busy}
              onClick={() => window.confirm(t('settings.email.removeKeyConfirm')) && void save({ secretAccessKey: '' })}
            >
              {t('settings.email.removeKey')}
            </button>
          </p>
        )}
        <label>
          {t('settings.email.from')}
          <input value={from} placeholder="KnowPod <noreply@example.com>" onChange={(e) => setFrom(e.target.value)} />
        </label>
        <p className="muted field-hint">{t('settings.email.fromHint')}</p>
        {error && <p className="error">{error}</p>}
        {saved && !error && <p className="success">{t('common.saved')}</p>}
        <button type="submit" disabled={busy}>
          {busy ? t('common.saving') : t('common.save')}
        </button>
      </form>

      <h3>{t('settings.email.testTitle')}</h3>
      <div className="form">
        <label>
          {t('settings.email.recipient')}
          <input type="email" value={recipient} placeholder="me@example.com" onChange={(e) => setRecipient(e.target.value)} />
        </label>
        <p className="field-hint">
          <button type="button" className="secondary-button" disabled={testing || !recipient.trim() || !current?.configured} onClick={() => void sendTest()}>
            {testing ? t('settings.email.sending') : t('settings.email.send')}
          </button>
        </p>
        {!current?.configured && <p className="muted field-hint">{t('settings.email.saveFirst')}</p>}
        {testResult && <p className={testResult.ok ? 'success' : 'error'}>{testResult.text}</p>}
      </div>
    </>
  );
}
