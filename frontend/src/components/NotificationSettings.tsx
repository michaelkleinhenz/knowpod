import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, NotificationStatus } from '../api/client';
import { desktopNotifications } from '../lib/desktop';
import { errorText } from '../lib/errors';
import { currentSubscription, deviceId, isDesktopApp, isInstalled, isIOS, pushSupported, subscribe, unsubscribe } from '../lib/push';
import { formatDate } from '../lib/recordings';

// deviceName makes a short name of a browser's user agent, e.g. "Safari on iPhone".
function deviceName(ua = ''): string {
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : '';
  return [browser, os].filter(Boolean).join(' · ') || ua || '—';
}

// NotificationSettings turns task reminders on or off for this browser (or installed app),
// sends a test, and lists the other devices that receive them.
// isBrave says whether this is Brave, which has the browser's push service off by default.
const isBrave = () => typeof navigator !== 'undefined' && 'brave' in navigator;

export function NotificationSettings() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<NotificationStatus | null>(null);
  const [thisDevice, setThisDevice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const supported = pushSupported();
  const permission = supported ? Notification.permission : 'default';

  const load = useCallback(async () => {
    try {
      const [st, sub] = await Promise.all([api.notifications(), currentSubscription()]);
      setStatus(st);
      setThisDevice(sub ? await deviceId(sub.endpoint) : null);
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [t]);
  useEffect(() => {
    void load();
  }, [load]);

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await fn();
      await load();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  const on = !!thisDevice && !!status?.devices.some((d) => d.id === thisDevice);
  const others = status?.devices.filter((d) => d.id !== thisDevice) ?? [];

  const enable = () =>
    run(async () => {
      const result = await subscribe(status!.publicKey!);
      if (result === 'denied') setNotice(t('notifications.denied'));
      if (result === 'unavailable') setNotice(t('notifications.noServiceWorker'));
      if (result === 'serviceError') setError(t(isBrave() ? 'notifications.serviceErrorBrave' : 'notifications.serviceError'));
    });
  const test = () =>
    run(async () => {
      const { sent } = await api.testNotification();
      setNotice(sent > 0 ? t('notifications.testSent', { count: sent }) : t('notifications.testNone'));
    });
  // A test goes to every device that receives notifications, so it can be sent from any
  // device (also an old desktop app, which can't receive them itself) as long as one does.
  const listening = status?.listening ?? 0;
  const canTest = (!!status?.available && (status?.devices.length ?? 0) > 0) || listening > 0;
  const testButton = (
    <button type="button" className={on ? 'primary-button' : 'secondary-button'} onClick={test} disabled={busy} title={t('notifications.testHint')}>
      {t('notifications.test')}
    </button>
  );

  return (
    <div className="notification-settings">
      <p className="muted">{t('notifications.intro')}</p>
      {desktopNotifications() ? (
        // The desktop app gets them over a live connection (lib/desktop.ts), not Web Push.
        <>
          <p className="notification-state">
            <span className={`status-dot${listening > 0 ? ' ok' : ''}`} aria-hidden="true" />
            {status && listening === 0 ? t('notifications.desktopConnecting') : t('notifications.desktopOn')}
          </p>
          {canTest && <div className="button-row">{testButton}</div>}
        </>
      ) : status && !status.available ? (
        <p className="notice">{t('notifications.unavailable')}</p>
      ) : !supported ? (
        <>
          <p className="notice">{isDesktopApp() ? t('notifications.desktop') : isIOS() && !isInstalled() ? t('notifications.iosInstall') : t('notifications.unsupported')}</p>
          {canTest && <div className="button-row">{testButton}</div>}
        </>
      ) : (
        <>
          <p className="notification-state">
            <span className={`status-dot${on ? ' ok' : ''}`} aria-hidden="true" />
            {on ? t('notifications.onHere') : permission === 'denied' ? t('notifications.blocked') : t('notifications.offHere')}
          </p>
          <div className="button-row">
            {on ? (
              <>
                {testButton}
                <button type="button" className="secondary-button" onClick={() => run(unsubscribe)} disabled={busy}>
                  {t('notifications.turnOff')}
                </button>
              </>
            ) : (
              <>
                <button type="button" className="primary-button" onClick={enable} disabled={busy || !status?.publicKey || permission === 'denied'}>
                  {t('notifications.turnOn')}
                </button>
                {canTest && testButton}
              </>
            )}
          </div>
        </>
      )}
      {notice && <p className="success">{notice}</p>}
      {error && <p className="error">{error}</p>}
      {others.length > 0 && (
        <>
          <h3 className="notification-devices-title">{t('notifications.otherDevices')}</h3>
          <ul className="notification-devices">
            {others.map((d) => (
              <li key={d.id}>
                <span>
                  {deviceName(d.userAgent)} <span className="muted">· {t('notifications.since', { date: formatDate(d.createdAt) })}</span>
                </span>
                <button type="button" className="small-button danger" disabled={busy} onClick={() => run(() => api.unsubscribePush(d.id))}>
                  {t('notifications.remove')}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
