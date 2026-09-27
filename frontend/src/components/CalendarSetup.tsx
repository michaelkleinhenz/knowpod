import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, CalendarSettings } from '../api/client';
import { errorText } from '../lib/errors';
import { formatDate } from '../lib/recordings';
import { CopyButton } from './CopyButton';

// CalendarSetup makes the secret link of the user's calendar feed, which calendar apps
// subscribe to. The link is shown once, when it is made; a new one replaces it.
export function CalendarSetup() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<CalendarSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.calendar().then(setSettings, (err) => setError(errorText(err, t)));
  }, [t]);

  async function act(fn: () => Promise<CalendarSettings>, confirmText?: string) {
    if (confirmText && !window.confirm(confirmText)) return;
    setBusy(true);
    setError(null);
    try {
      setSettings(await fn());
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }
  const enable = () => act(api.enableCalendar, settings?.enabled ? t('calendar.resetConfirm') : undefined);
  const disable = () =>
    act(async () => {
      await api.disableCalendar();
      return { enabled: false };
    }, t('calendar.disableConfirm'));

  if (!settings) return error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>;
  const url = settings.feedPath ? window.location.origin + settings.feedPath : null;
  return (
    <>
      <p className="muted">{t('calendar.intro')}</p>
      {url && (
        <>
          <dl className="facts">
            <dt>{t('calendar.link')}</dt>
            <dd className="with-action">
              <code>{url}</code> <CopyButton text={url} />
            </dd>
          </dl>
          <p className="notice">{t('calendar.linkOnce')}</p>
          <ol className="steps">
            <li>{t('calendar.stepGoogle')}</li>
            <li>{t('calendar.stepApple')}</li>
            <li>{t('calendar.stepOutlook')}</li>
          </ol>
        </>
      )}
      {settings.enabled && !url && <p>{t('calendar.enabledSince', { date: formatDate(settings.createdAt) })}</p>}
      {error && <p className="error">{error}</p>}
      <div className="button-row">
        <button type="button" className="primary-button" disabled={busy} onClick={() => void enable()}>
          {settings.enabled ? t('calendar.reset') : t('calendar.enable')}
        </button>
        {settings.enabled && (
          <button type="button" className="secondary-button danger" disabled={busy} onClick={() => void disable()}>
            {t('calendar.disable')}
          </button>
        )}
      </div>
    </>
  );
}
