import { useCallback, useEffect, useRef, useState } from 'react';
import type { TFunction } from 'i18next';
import { Trans, useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { pocketBluetooth, PocketBluetoothResult } from '../lib/desktop';
import { bluetoothErrorText } from './PocketBluetooth';

// How often the dialog asks the desktop app how copying goes.
const pollInterval = 1_000;

// pocketUsbSyncAvailable says whether the Pocket USB Sync button is shown: only in the
// desktop app, and only in one that can switch the Pocket's USB drive on over Bluetooth.
export const pocketUsbSyncAvailable = () => !!pocketBluetooth();

// PocketUsbSyncDialog walks through copying from the Pocket by USB. The order matters: the
// Pocket starts as a USB drive only when its drive was switched on over Bluetooth *before*
// the cable was plugged in, so the dialog has it unplugged, switched on, then plugged in. The
// desktop app finds the drive and copies the new recordings by itself (desktop/src/pocket.js);
// the dialog shows how that goes. Closing it ejects the drive, so the Pocket can be unplugged
// safely (once a copy that still runs is done).
export function PocketUsbSyncDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const [call] = useState(() => pocketBluetooth()!);
  const [state, setState] = useState<PocketBluetoothResult | null>(null);
  const [turningOn, setTurningOn] = useState(false);
  const [usbOn, setUsbOn] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const connected = useRef(false);
  connected.current = !!state?.connected;

  const close = useCallback(() => {
    if (connected.current) void call({ action: 'eject' }).catch(() => undefined);
    onClose();
  }, [call, onClose]);

  useEffect(() => {
    let stopped = false;
    const poll = () =>
      call({ action: 'state' }).then(
        (s) => !stopped && setState(s),
        () => undefined,
      );
    void poll();
    const timer = setInterval(poll, pollInterval);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [call]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [close]);

  async function turnOn() {
    setTurningOn(true);
    setError(null);
    try {
      const r = await call({ action: 'usb-on' });
      if (r.ok) setUsbOn(true);
      else setError(bluetoothErrorText(r, t));
    } catch (err) {
      setError(t('pocketBluetooth.errors.failed', { detail: err instanceof Error ? err.message : String(err) }));
    } finally {
      setTurningOn(false);
    }
  }

  const heading = t('pocketUsbSync.title');
  let body;
  if (!state) {
    body = <p className="muted">{t('common.loading')}</p>;
  } else if (!state.configured && !state.connected) {
    body = (
      <p>
        <Trans i18nKey="pocketUsbSync.notConfigured" components={{ 1: <Link to="/settings?tab=account#pocket" onClick={onClose} /> }} />
      </p>
    );
  } else if (state.connected) {
    body = (
      <>
        <p>{progressText(state, t)}</p>
        {/* Copying again only does something when copying is off or the last copy didn't
            finish: once it's done, every recording on the Pocket is in knowpod. */}
        {!state.syncing && (!state.enabled || !['done', 'checking', 'copying'].includes(state.phase ?? '')) && (
          <div className="new-item-actions">
            <button type="button" className="primary-button" onClick={() => void call({ action: 'sync' })}>
              {t('pocketUsbSync.copyNow')}
            </button>
          </div>
        )}
      </>
    );
  } else {
    body = (
      <>
        <p className="pocket-usb-order">{t('pocketUsbSync.order')}</p>
        <ol className="steps pocket-usb-steps">
          <li>{t('pocketUsbSync.step1')}</li>
          <li>
            {t('pocketUsbSync.step2')}
            <div>
              {usbOn ? (
                <span className="success">✓ {t('pocketUsbSync.usbIsOn')}</span>
              ) : (
                <button type="button" className="primary-button" disabled={turningOn || state.busy} onClick={() => void turnOn()}>
                  {turningOn ? t('pocketBluetooth.turningOn') : t('pocketBluetooth.turnOn')}
                </button>
              )}
            </div>
            {error && <p className="error">{error}</p>}
          </li>
          <li className={usbOn ? undefined : 'muted'}>
            {t('pocketUsbSync.step3')}
            {usbOn && <div className="muted">{t('pocketUsbSync.waiting')}</div>}
          </li>
        </ol>
      </>
    );
  }

  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="new-item-dialog" role="dialog" aria-modal="true" aria-label={heading}>
        <h2>{heading}</h2>
        {body}
        <div className="new-item-actions">
          <button type="button" className="secondary-button" onClick={close}>
            {state?.connected ? t('pocketUsbSync.closeEject') : t('pocketUsbSync.close')}
          </button>
        </div>
      </div>
    </div>
  );
}

// progressText says how copying from the plugged-in Pocket goes.
function progressText(s: PocketBluetoothResult, t: TFunction): string {
  if (!s.enabled) return t('pocketUsbSync.disabled');
  switch (s.phase) {
    case 'checking':
      return t('pocketUsbSync.checking');
    case 'copying':
      return t('pocketUsbSync.copying', { current: s.current, total: s.total });
    case 'done':
      return s.copied ? t('pocketUsbSync.copied', { count: s.copied }) : t('pocketUsbSync.upToDate');
    case 'signed-out':
      return t('pocketUsbSync.signedOut');
    case 'failed':
      return t('pocketUsbSync.failed');
    default:
      return s.syncing ? t('pocketUsbSync.checking') : t('pocketUsbSync.connected');
  }
}
