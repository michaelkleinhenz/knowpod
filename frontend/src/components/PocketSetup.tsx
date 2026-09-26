import { FormEvent, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, PocketSettings } from '../api/client';
import { errorText } from '../lib/errors';
import { CopyButton } from './CopyButton';

// PocketSetup lets the signed-in user connect their Pocket recorder (heypocketai.com): it
// shows their personal webhook URL and stores their webhook signing secret and API key.
export function PocketSetup() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<PocketSettings | null>(null);
  const [secret, setSecret] = useState('');
  const [apiKey, setAPIKey] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.pocket().then(setSettings, (e) => setError(errorText(e, t)));
  }, [t]);

  async function save(update: { webhookSecret?: string; apiKey?: string }) {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      setSettings(await api.savePocket(update));
      setSecret('');
      setAPIKey('');
      setSaved(true);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const update: { webhookSecret?: string; apiKey?: string } = {};
    if (secret.trim()) update.webhookSecret = secret.trim();
    if (apiKey.trim()) update.apiKey = apiKey.trim();
    if (Object.keys(update).length) save(update);
  }

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>;
  const url = window.location.origin + settings.webhookPath;
  const ready = settings.webhookSecretConfigured && settings.apiKeyConfigured;

  return (
    <>
      <p className="muted">{t('pocket.intro')}</p>
      <p>
        <span className={`status-pill ${ready ? 'ok' : 'bad'}`}>{ready ? t('pocket.connected') : t('pocket.notConnected')}</span>
      </p>
      <dl className="facts">
        <dt>{t('pocket.webhookUrl')}</dt>
        <dd className="with-action">
          <code>{url}</code> <CopyButton text={url} />
        </dd>
      </dl>
      <ol className="steps">
        <li>{t('pocket.step1')}</li>
        <li>{t('pocket.step2')}</li>
        <li>{t('pocket.step3')}</li>
      </ol>
      <form onSubmit={handleSubmit} className="form">
        <label>
          {t('pocket.secret')}
          <input
            type="password"
            autoComplete="off"
            placeholder={settings.webhookSecretConfigured ? t('pocket.secretPlaceholderSet') : t('pocket.secretPlaceholder')}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
          />
        </label>
        <label>
          {t('pocket.apiKey')}
          <input
            type="password"
            autoComplete="off"
            placeholder={settings.apiKeyConfigured ? t('pocket.apiKeyPlaceholderSet', { hint: settings.apiKeyHint ?? '…' }) : 'pk_…'}
            value={apiKey}
            onChange={(e) => setAPIKey(e.target.value)}
          />
        </label>
        {error && <p className="error">{error}</p>}
        {saved && <p className="success">{t('common.saved')}</p>}
        <div className="button-row">
          <button type="submit" disabled={busy || (!secret.trim() && !apiKey.trim())}>
            {busy ? t('common.saving') : t('common.save')}
          </button>
          {(settings.webhookSecretConfigured || settings.apiKeyConfigured) && (
            <button
              type="button"
              className="secondary-button danger"
              disabled={busy}
              onClick={() => window.confirm(t('pocket.disconnectConfirm')) && save({ webhookSecret: '', apiKey: '' })}
            >
              {t('pocket.disconnect')}
            </button>
          )}
        </div>
      </form>
    </>
  );
}
