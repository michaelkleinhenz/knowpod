import { FormEvent, useEffect, useState } from 'react';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { appContext, pocketBluetooth, PocketBluetoothResult } from '../lib/desktop';
import { CopyButton } from './CopyButton';

// errorKeys are the explained failures of the app's Pocket Bluetooth call.
const errorKeys = [
  'not-configured',
  'not-found',
  'auth',
  'unsupported',
  'permission',
  'usb-refused',
  'busy',
  'timeout',
  'invalid-address',
  'invalid-key',
  'activated-elsewhere',
  'activate-no-answer',
  'address-needed',
  'several',
];

// bluetoothErrorText explains a failed Pocket Bluetooth call.
export function bluetoothErrorText(r: PocketBluetoothResult, t: TFunction): string {
  return r.error && errorKeys.includes(r.error)
    ? t(`pocketBluetooth.errors.${r.error}`, { context: appContext() })
    : t('pocketBluetooth.errors.failed', { detail: r.message || r.error || '' });
}

// PocketBluetooth sets up the app's Bluetooth connection to a Pocket recorder: the recorder is
// a USB drive only until it's unplugged once, so the desktop app switches the drive on again
// over Bluetooth before copying from it; over Bluetooth, the desktop and Android apps also
// raise the Pocket's WiFi to copy over it. Only in the apps; the address and session key stay
// there. A factory-reset Pocket can be activated here with a new key, as the Pocket app does;
// Remove forgets it (the Pocket keeps its key until it's reset again). Show key fetches the key
// from the app, so it can be backed up: without it, a lost app means resetting the Pocket.
export function PocketBluetooth() {
  const { t } = useTranslation();
  // Looked up once: the effect below must not run again on every render.
  const [call] = useState(() => pocketBluetooth());
  const [settings, setSettings] = useState<PocketBluetoothResult | null>(null);
  const [address, setAddress] = useState('');
  const [sessionKey, setSessionKey] = useState('');
  const [activateAddress, setActivateAddress] = useState('');
  const [busy, setBusy] = useState<'save' | 'check' | 'usb-on' | 'activate' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [info, setInfo] = useState<PocketBluetoothResult | null>(null);
  // The session key while it's shown; fetched only when asked for, and hidden again on any change.
  const [shownKey, setShownKey] = useState<string | null>(null);

  useEffect(() => {
    call?.({ action: 'settings' }).then((s) => {
      setSettings(s);
      setAddress(s.address ?? '');
    });
  }, [call]);

  if (!call) return null;
  if (!settings) return <p className="muted">{t('common.loading')}</p>;

  async function run(action: 'save' | 'check' | 'usb-on', update?: { address?: string; sessionKey?: string }) {
    setBusy(action);
    setError(null);
    if (action === 'save') setShownKey(null);
    setNotice(null);
    if (action === 'check') setInfo(null);
    try {
      const r = await call!(action === 'save' ? { action, ...update } : { action });
      if (!r.ok) {
        setError(bluetoothErrorText(r, t));
        return;
      }
      if (action === 'save') {
        setSettings(r);
        setAddress(r.address ?? '');
        setSessionKey('');
        setNotice(t('common.saved'));
      } else if (action === 'check') {
        setInfo(r);
      } else {
        setNotice(t('pocketBluetooth.usbOn'));
      }
    } catch (err) {
      setError(t('pocketBluetooth.errors.failed', { detail: err instanceof Error ? err.message : String(err) }));
    } finally {
      setBusy(null);
    }
  }

  async function showKey() {
    setError(null);
    setNotice(null);
    try {
      const r = await call!({ action: 'key' });
      if (!r.ok || !r.sessionKey) {
        setError(bluetoothErrorText(r, t));
        return;
      }
      setShownKey(r.sessionKey);
    } catch (err) {
      setError(t('pocketBluetooth.errors.failed', { detail: err instanceof Error ? err.message : String(err) }));
    }
  }

  async function activate() {
    if (!window.confirm(t('pocketBluetooth.activateConfirm', { context: appContext() }))) return;
    setBusy('activate');
    setError(null);
    setShownKey(null);
    setNotice(null);
    setInfo(null);
    try {
      const r = await call!({ action: 'activate', address: activateAddress.trim() });
      if (!r.ok) {
        setError(bluetoothErrorText(r, t));
        // Without an answer the new key stays saved: show it as set.
        const s = await call!({ action: 'settings' });
        setSettings(s);
        setAddress(s.address ?? '');
        return;
      }
      setSettings(r);
      setAddress(r.address ?? '');
      setActivateAddress('');
      setNotice(r.verified ? t('pocketBluetooth.activated') : t('pocketBluetooth.activatedUnchecked'));
      if (r.verified) setInfo(r);
    } catch (err) {
      setError(t('pocketBluetooth.errors.failed', { detail: err instanceof Error ? err.message : String(err) }));
    } finally {
      setBusy(null);
    }
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const update: { address?: string; sessionKey?: string } = {};
    if (address.trim() !== (settings?.address ?? '')) update.address = address.trim();
    if (sessionKey.trim()) update.sessionKey = sessionKey.trim();
    if (Object.keys(update).length) void run('save', update);
  }

  const configured = !!settings.address && !!settings.sessionKeySet;
  const usbSupported = settings.usbSupported !== false;
  const context = appContext();
  const changed = address.trim() !== (settings.address ?? '') || !!sessionKey.trim();
  const mb = (kb: number) => (kb / 1024).toLocaleString(undefined, { maximumFractionDigits: 0 });
  const canActivate = !!settings.activateSupported && !settings.address && !settings.sessionKeySet;
  const findsAddress = settings.activateFindsAddress !== false;

  return (
    <>
      <h3 className="subheading">{t('pocketBluetooth.title', { context })}</h3>
      <p className="muted">{t('pocketBluetooth.intro', { context })}</p>
      {canActivate && (
        <div className="form">
          <h4 className="subheading">{t('pocketBluetooth.activateTitle')}</h4>
          <p className="muted">{t('pocketBluetooth.activateIntro')}</p>
          <p className="muted">{t('pocketBluetooth.activateSteps')}</p>
          <label>
            {findsAddress ? t('pocketBluetooth.activateAddress') : t('pocketBluetooth.activateAddressRequired')}
            <input
              autoComplete="off"
              spellCheck={false}
              placeholder="AA:BB:CC:DD:EE:FF"
              value={activateAddress}
              onChange={(e) => setActivateAddress(e.target.value)}
            />
          </label>
          {findsAddress && <p className="muted">{t('pocketBluetooth.activateAddressHint')}</p>}
          <div className="button-row">
            <button
              type="button"
              className="primary-button"
              disabled={!!busy || (!findsAddress && !activateAddress.trim())}
              onClick={() => void activate()}
            >
              {busy === 'activate' ? t('pocketBluetooth.activating') : t('pocketBluetooth.activate')}
            </button>
          </div>
          <h4 className="subheading">{t('pocketBluetooth.manualTitle')}</h4>
        </div>
      )}
      <form onSubmit={handleSubmit} className="form">
        <label>
          {t('pocketBluetooth.address')}
          <input
            autoComplete="off"
            spellCheck={false}
            placeholder="AA:BB:CC:DD:EE:FF"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
          />
        </label>
        <label>
          {t('pocketBluetooth.sessionKey')}
          <input
            type="password"
            autoComplete="off"
            placeholder={settings.sessionKeySet ? t('pocketBluetooth.sessionKeySet') : t('pocketBluetooth.sessionKeyPlaceholder')}
            value={sessionKey}
            onChange={(e) => setSessionKey(e.target.value)}
          />
        </label>
        {settings.sessionKeySet && settings.keySupported && (
          <div>
            {shownKey ? (
              <>
                <p>
                  <code>{shownKey}</code> <CopyButton text={shownKey} />{' '}
                  <button type="button" className="small-button" onClick={() => setShownKey(null)}>
                    {t('pocketBluetooth.hideKey')}
                  </button>
                </p>
                <p className="muted">{t('pocketBluetooth.keyBackupHint')}</p>
              </>
            ) : (
              <button type="button" className="small-button" disabled={!!busy} onClick={() => void showKey()}>
                {t('pocketBluetooth.showKey')}
              </button>
            )}
          </div>
        )}
        <p className="muted">{t('pocketBluetooth.hint', { context })}</p>
        {error && <p className="error">{error}</p>}
        {notice && <p className="success">{notice}</p>}
        {info && (
          <dl className="facts">
            <dt>{t('pocketBluetooth.battery')}</dt>
            <dd>{info.battery != null ? `${info.battery} %` : '–'}</dd>
            <dt>{t('pocketBluetooth.firmware')}</dt>
            <dd>{info.firmware || '–'}</dd>
            <dt>{t('pocketBluetooth.storage')}</dt>
            <dd>{info.storage ? t('pocketBluetooth.storageValue', { used: mb(info.storage.usedKB), total: mb(info.storage.totalKB) }) : '–'}</dd>
            {usbSupported && (
              <>
                <dt>{t('pocketBluetooth.usbDrive')}</dt>
                <dd>{info.usb == null ? '–' : info.usb ? t('pocketBluetooth.on') : t('pocketBluetooth.off')}</dd>
              </>
            )}
          </dl>
        )}
        <div className="button-row">
          <button type="submit" disabled={!!busy || !changed}>
            {busy === 'save' ? t('common.saving') : t('common.save')}
          </button>
          <button type="button" className="secondary-button" disabled={!!busy || !configured || changed} onClick={() => void run('check')}>
            {busy === 'check' ? t('pocketBluetooth.checking') : t('pocketBluetooth.check')}
          </button>
          {usbSupported && (
            <button type="button" className="secondary-button" disabled={!!busy || !configured || changed} onClick={() => void run('usb-on')}>
              {busy === 'usb-on' ? t('pocketBluetooth.turningOn') : t('pocketBluetooth.turnOn')}
            </button>
          )}
          {(settings.address || settings.sessionKeySet) && (
            <button
              type="button"
              className="secondary-button danger"
              disabled={!!busy}
              onClick={() => window.confirm(t('pocketBluetooth.removeConfirm', { context })) && void run('save', { address: '', sessionKey: '' })}
            >
              {t('pocketBluetooth.remove')}
            </button>
          )}
        </div>
      </form>
    </>
  );
}
