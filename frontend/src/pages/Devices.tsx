import { FormEvent, useCallback, useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { api, Device } from '../api/client';
import { CopyButton } from '../components/CopyButton';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';

const DATE_TIME: Intl.DateTimeFormatOptions = { dateStyle: 'medium', timeStyle: 'short' };

// Devices lists the user's recorders and lets them add and remove them. Tokens are only
// available right after they are issued, so they are kept in page state and shown in the
// device's row until the user leaves the page.
export function Devices() {
  const { t } = useTranslation();
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [tokens, setTokens] = useState<Record<string, string>>({});
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null); // "add" or the device ID being changed

  const load = useCallback(async () => {
    try {
      const all = await api.devices();
      setDevices(all.filter((d) => !d.revokedAt));
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleAdd(e: FormEvent) {
    e.preventDefault();
    setBusy('add');
    setError(null);
    try {
      const { device, token } = await api.createDevice(name.trim());
      setTokens((tk) => ({ ...tk, [device.id]: token }));
      setName('');
      await load();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(null);
    }
  }

  async function handleRotate(d: Device) {
    if (!window.confirm(t('devices.rotateConfirm', { name: d.name }))) return;
    setBusy(d.id);
    setError(null);
    try {
      const { token } = await api.rotateDeviceToken(d.id);
      setTokens((tk) => ({ ...tk, [d.id]: token }));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(null);
    }
  }

  async function handleRemove(d: Device) {
    if (!window.confirm(t('devices.removeConfirm', { name: d.name }))) return;
    setBusy(d.id);
    setError(null);
    try {
      await api.removeDevice(d.id);
      setTokens(({ [d.id]: _removed, ...rest }) => rest);
      await load();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(null);
    }
  }

  const uploadsURL = `${window.location.origin}/api/v1/uploads`;

  return (
    <div className="page">
      <section className="card">
        <h1>{t('devices.title')}</h1>
        <p className="muted">
          <Trans
            i18nKey="devices.intro"
            values={{ url: uploadsURL }}
            components={{ 1: <code />, 3: <code />, 5: <Link to="/status" /> }}
          />
        </p>

        <form onSubmit={handleAdd} className="inline-form">
          <label className="sr-only" htmlFor="device-name">
            {t('devices.nameLabel')}
          </label>
          <input
            id="device-name"
            placeholder={t('devices.namePlaceholder')}
            required
            maxLength={100}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <button type="submit" disabled={busy === 'add' || !name.trim()}>
            {busy === 'add' ? t('devices.adding') : t('devices.add')}
          </button>
        </form>
        {error && <p className="error">{error}</p>}
      </section>

      <section className="card">
        {!devices && !error && <p className="muted">{t('common.loading')}</p>}
        {devices && devices.length === 0 && <p className="muted">{t('devices.empty')}</p>}
        {devices && devices.length > 0 && (
          <ul className="device-list">
            {devices.map((d) => {
              const token = tokens[d.id];
              return (
                <li key={d.id} className={token ? 'device new-token' : 'device'}>
                  <div className="device-head">
                    <div>
                      <div className="device-name">{d.name}</div>
                      <div className="device-meta muted">
                        {t('devices.added', { date: formatDate(d.createdAt, DATE_TIME) })} ·{' '}
                        {t('devices.lastSeen', { date: formatDate(d.lastSeenAt, DATE_TIME) })}
                      </div>
                    </div>
                    <div className="device-actions">
                      <button type="button" className="small-button" disabled={busy === d.id} onClick={() => handleRotate(d)}>
                        {t('devices.newToken')}
                      </button>
                      <button type="button" className="small-button danger" disabled={busy === d.id} onClick={() => handleRemove(d)}>
                        {t('devices.remove')}
                      </button>
                    </div>
                  </div>
                  <div className="device-token">
                    <span className="token-label">{t('devices.apiToken')}</span>
                    {token ? (
                      <>
                        <code className="token">{token}</code>
                        <CopyButton text={token} />
                        <p className="token-note">{t('devices.tokenNote')}</p>
                      </>
                    ) : (
                      <span className="muted">{t('devices.tokenHidden')}</span>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
