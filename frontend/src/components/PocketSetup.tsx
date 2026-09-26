import { FormEvent, useEffect, useState } from 'react';
import { api, PocketSettings } from '../api/client';
import { CopyButton } from './CopyButton';

// PocketSetup lets the signed-in user connect their Pocket recorder (heypocketai.com): it
// shows their personal webhook URL and stores their webhook signing secret and API key.
export function PocketSetup() {
  const [settings, setSettings] = useState<PocketSettings | null>(null);
  const [secret, setSecret] = useState('');
  const [apiKey, setAPIKey] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.pocket().then(setSettings, (e: Error) => setError(e.message));
  }, []);

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
      setError((err as Error).message);
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

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">Loading…</p>;
  const url = window.location.origin + settings.webhookPath;
  const ready = settings.webhookSecretConfigured && settings.apiKeyConfigured;

  return (
    <>
      <p className="muted">
        Connect your Pocket recorder (heypocketai.com): Pocket announces new recordings to your personal webhook URL, and
        knowpod downloads the audio with your API key. The recordings appear under Conversations.
      </p>
      <p>
        <span className={`status-pill ${ready ? 'ok' : 'bad'}`}>{ready ? 'Connected' : 'Not connected'}</span>
      </p>
      <dl className="facts">
        <dt>Your webhook URL</dt>
        <dd className="with-action">
          <code>{url}</code> <CopyButton text={url} />
        </dd>
      </dl>
      <ol className="steps">
        <li>In the Pocket app's integrations settings, add a webhook with the URL above.</li>
        <li>Pocket shows the webhook's signing secret once. Paste it below.</li>
        <li>Create a Pocket API key and paste it below.</li>
      </ol>
      <form onSubmit={handleSubmit} className="form">
        <label>
          Webhook signing secret
          <input
            type="password"
            autoComplete="off"
            placeholder={settings.webhookSecretConfigured ? 'Set. Enter a new secret to replace it.' : 'From the Pocket webhook dialog'}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
          />
        </label>
        <label>
          Pocket API key
          <input
            type="password"
            autoComplete="off"
            placeholder={settings.apiKeyConfigured ? `Set (${settings.apiKeyHint ?? 'hidden'}). Enter a new key to replace it.` : 'pk_…'}
            value={apiKey}
            onChange={(e) => setAPIKey(e.target.value)}
          />
        </label>
        {error && <p className="error">{error}</p>}
        {saved && <p className="success">Saved.</p>}
        <div className="button-row">
          <button type="submit" disabled={busy || (!secret.trim() && !apiKey.trim())}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          {(settings.webhookSecretConfigured || settings.apiKeyConfigured) && (
            <button
              type="button"
              className="secondary-button danger"
              disabled={busy}
              onClick={() => window.confirm('Disconnect Pocket? New Pocket recordings stop arriving.') && save({ webhookSecret: '', apiKey: '' })}
            >
              Disconnect
            </button>
          )}
        </div>
      </form>
    </>
  );
}
