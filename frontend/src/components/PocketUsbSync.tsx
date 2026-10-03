import { useCallback, useEffect, useRef, useState } from 'react';
import type { TFunction } from 'i18next';
import { Trans, useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { pocketBluetooth, PocketBluetoothRequest, PocketBluetoothResult, PocketWifiState } from '../lib/desktop';
import { bluetoothErrorText } from './PocketBluetooth';

// How often the dialog asks the desktop app how copying goes.
const pollInterval = 1_000;

// pocketUsbSyncAvailable says whether the Pocket Sync button is shown: only in the desktop
// app, and only in one that can talk to the Pocket over Bluetooth.
export const pocketUsbSyncAvailable = () => !!pocketBluetooth();

type Method = 'usb' | 'wifi';
const methods: readonly Method[] = ['usb', 'wifi'];
// The method picked last, kept in this browser.
const methodStorageKey = 'knowpod.pocketSyncMethod';

function storedMethod(): Method | null {
  try {
    const value = window.localStorage.getItem(methodStorageKey);
    return value === 'usb' || value === 'wifi' ? value : null;
  } catch {
    return null;
  }
}

function storeMethod(method: Method) {
  try {
    window.localStorage.setItem(methodStorageKey, method);
  } catch {
    // not kept: fine
  }
}

type Call = (request: PocketBluetoothRequest) => Promise<PocketBluetoothResult>;

// PocketUsbSyncDialog copies new recordings from the Pocket, by USB or over the Pocket's WiFi.
// The desktop app does the copying (desktop/src/pocket.js, desktop/src/pocket-wifi-sync.js);
// the dialog starts it and shows how it goes. Closing the dialog ejects a plugged-in Pocket
// (once a copy that still runs is done); a WiFi copy keeps running and reports by a
// notification when it's done.
export function PocketUsbSyncDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const [call] = useState(() => pocketBluetooth()!);
  const [state, setState] = useState<PocketBluetoothResult | null>(null);
  // picked is the method chosen in this dialog; until then, what is going on decides.
  const [picked, setPicked] = useState<Method | null>(null);
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

  // Older desktop apps can't copy over WiFi: they don't report wifiSupported at all.
  const wifiKnown = state?.wifiSupported !== undefined;
  const method: Method = !wifiKnown
    ? 'usb'
    : (picked ?? (state?.wifi?.running ? 'wifi' : state?.connected ? 'usb' : (storedMethod() ?? 'usb')));
  const pick = (m: Method) => {
    setPicked(m);
    storeMethod(m);
  };

  const heading = t('pocketUsbSync.title');
  const ready = !!state && (state.configured || state.connected);
  let body;
  if (!state) {
    body = <p className="muted">{t('common.loading')}</p>;
  } else if (!ready) {
    body = (
      <p>
        <Trans i18nKey="pocketUsbSync.notConfigured" components={{ 1: <Link to="/settings?tab=account#pocket" onClick={onClose} /> }} />
      </p>
    );
  } else if (method === 'wifi') {
    body = <WifiPane state={state} call={call} />;
  } else {
    body = <UsbPane state={state} call={call} />;
  }

  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="new-item-dialog pocket-sync-dialog" role="dialog" aria-modal="true" aria-label={heading}>
        <h2>{heading}</h2>
        {ready && wifiKnown && (
          <div className="segmented" role="tablist" aria-label={t('pocketUsbSync.method')}>
            {methods.map((m) => (
              <button
                key={m}
                type="button"
                role="tab"
                aria-selected={method === m}
                className={method === m ? 'active' : undefined}
                onClick={() => pick(m)}
              >
                {m === 'usb' ? t('pocketUsbSync.usb') : t('pocketWifiSync.tab')}
              </button>
            ))}
          </div>
        )}
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

// UsbPane walks through copying by USB. The order matters: the Pocket starts as a USB drive
// only when its drive was switched on over Bluetooth *before* the cable was plugged in, so the
// steps have it unplugged, switched on, then plugged in.
function UsbPane({ state, call }: { state: PocketBluetoothResult; call: Call }) {
  const { t } = useTranslation();
  const [turningOn, setTurningOn] = useState(false);
  const [usbOn, setUsbOn] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  if (state.connected) {
    return (
      <>
        <p>{usbProgressText(state, t)}</p>
        {/* Once a copy is done, checking again picks up what came since or was missed. */}
        {!state.syncing && (!state.enabled || !['checking', 'copying'].includes(state.phase ?? '')) && (
          <div className="new-item-actions">
            <button type="button" className="primary-button" onClick={() => void call({ action: 'sync' })}>
              {state.enabled && state.phase === 'done' ? t('pocketUsbSync.checkAgain') : t('pocketUsbSync.copyNow')}
            </button>
          </div>
        )}
      </>
    );
  }
  return (
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

// usbProgressText says how copying from the plugged-in Pocket goes.
function usbProgressText(s: PocketBluetoothResult, t: TFunction): string {
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

// wifiErrorKeys are the explained failures of the WiFi copy (desktop/src/pocket-wifi-sync.js).
const wifiErrorKeys = [
  'not-configured',
  'not-found',
  'auth',
  'unsupported',
  'busy',
  'timeout',
  'disconnected',
  'no-answer',
  'firmware',
  'battery',
  'wifi-unsupported',
  'wifi-setup',
  'wifi-join',
  'wifi-ap',
  'transfer',
  'no-server',
  'signed-out',
  'offline',
  'server',
];

// wifiErrorText explains why the WiFi copy (or one of its recordings) failed; message is the
// desktop app's detail, shown where it helps.
function wifiErrorText(error: string, message: string, t: TFunction): string {
  if (wifiErrorKeys.includes(error)) return t(`pocketWifiSync.errors.${error}`, { detail: message });
  return t('pocketWifiSync.errors.failed', { detail: message || error });
}

const megabytes = (bytes: number) => (bytes / 1_000_000).toLocaleString(undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 });

// WifiPane copies over the Pocket's WiFi: the desktop app raises it, moves this computer onto
// it for the transfer and back, and uploads the new recordings afterwards.
function WifiPane({ state, call }: { state: PocketBluetoothResult; call: Call }) {
  const { t } = useTranslation();
  const [startError, setStartError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const wifi = state.wifi;

  async function start() {
    setStarting(true);
    setStartError(null);
    try {
      const r = await call({ action: 'wifi-sync' });
      if (!r.ok) setStartError(wifiErrorText(r.error ?? 'failed', r.message ?? '', t));
    } catch (err) {
      setStartError(t('pocketWifiSync.errors.failed', { detail: err instanceof Error ? err.message : String(err) }));
    } finally {
      setStarting(false);
    }
  }

  if (!state.wifiSupported) return <p>{t('pocketWifiSync.unsupported')}</p>;
  if (wifi?.running) return <WifiProgress wifi={wifi} call={call} />;

  const startButton = (label: string) => (
    <div className="new-item-actions">
      <button type="button" className="primary-button" disabled={starting || state.busy} onClick={() => void start()}>
        {starting ? t('pocketWifiSync.starting') : label}
      </button>
    </div>
  );

  // The outcome of the last copy, while the desktop app runs.
  if (wifi?.phase === 'done') {
    return (
      <>
        {wifi.copied ? (
          <p className="success">{t('pocketWifiSync.copied', { count: wifi.copied })}</p>
        ) : wifi.found === undefined ? (
          <p className="success">{t('pocketWifiSync.upToDateLegacy')}</p>
        ) : !wifi.found ? (
          <p className="error">{t('pocketWifiSync.noneFound')}</p>
        ) : (
          !wifi.incomplete && <p className="success">{t('pocketWifiSync.upToDate', { count: wifi.found })}</p>
        )}
        {!!wifi.found && wifi.incomplete && <p className="error">{t('pocketWifiSync.incomplete', { count: wifi.found })}</p>}
        {wifi.failed > 0 && (
          <p className="error">
            {t('pocketWifiSync.someFailed', { count: wifi.failed })} {wifiErrorText(wifi.error, wifi.message, t)}
          </p>
        )}
        {startError && <p className="error">{startError}</p>}
        {state.busy && <p className="muted">{t('pocketWifiSync.busy')}</p>}
        {startButton(wifi.copied || wifi.failed ? t('pocketWifiSync.again') : t('pocketWifiSync.checkAgain'))}
      </>
    );
  }
  if (wifi?.phase === 'failed') {
    return (
      <>
        {wifi.error === 'cancelled' ? (
          <p>{t('pocketWifiSync.cancelled', { count: wifi.copied })}</p>
        ) : (
          <>
            <p className="error">{wifiErrorText(wifi.error, wifi.message, t)}</p>
            {wifi.copied > 0 && <p>{t('pocketWifiSync.copied', { count: wifi.copied })}</p>}
          </>
        )}
        {startError && <p className="error">{startError}</p>}
        {state.busy && <p className="muted">{t('pocketWifiSync.busy')}</p>}
        {startButton(t('pocketWifiSync.retry'))}
      </>
    );
  }
  return (
    <>
      <p>{t('pocketWifiSync.intro')}</p>
      <ul className="pocket-wifi-notes">
        <li>{t('pocketWifiSync.noteFirmware')}</li>
        <li>{t('pocketWifiSync.noteNetwork')}</li>
        <li>{t('pocketWifiSync.noteApp')}</li>
      </ul>
      {startError && <p className="error">{startError}</p>}
      {state.busy && <p className="muted">{t('pocketWifiSync.busy')}</p>}
      {startButton(t('pocketWifiSync.start'))}
    </>
  );
}

// wifiSteps are the phases in order, for the step counter; restarting the Pocket's WiFi is
// part of downloading.
const wifiSteps = ['connecting', 'listing', 'checking', 'wifi-starting', 'downloading', 'reconnecting', 'uploading'];

// WifiProgress shows a WiFi copy that runs: the step, a progress bar, and for the transfer
// itself the megabytes and the speed.
function WifiProgress({ wifi, call }: { wifi: PocketWifiState; call: Call }) {
  const { t } = useTranslation();
  const phase = wifi.phase === 'wifi-restarting' ? 'downloading' : wifi.phase;
  const step = Math.max(0, wifiSteps.indexOf(phase)) + 1;
  // Off the usual network: no internet until the transfer is done.
  const offline = ['wifi-starting', 'downloading', 'wifi-restarting'].includes(wifi.phase);
  let bar;
  let detail: string | null = null;
  if (wifi.phase === 'downloading' && wifi.totalBytes > 0) {
    bar = <progress value={wifi.bytes} max={wifi.totalBytes} />;
    detail = t('pocketWifiSync.bytes', {
      done: megabytes(wifi.bytes),
      total: megabytes(wifi.totalBytes),
      rate: wifi.rate > 0 ? megabytes(wifi.rate) : '–',
    });
  } else if (wifi.phase === 'uploading' && wifi.total > 0) {
    bar = <progress value={wifi.current - 1} max={wifi.total} />;
  } else {
    bar = <progress />; // indeterminate: there's no measure of how long this takes
  }
  return (
    <>
      <p className="muted pocket-wifi-step">{t('pocketWifiSync.step', { step, steps: wifiSteps.length })}</p>
      <p>{wifiPhaseText(wifi, t)}</p>
      <div className="pocket-wifi-progress">{bar}</div>
      {detail && <p className="muted">{detail}</p>}
      <p className="muted">{offline ? t('pocketWifiSync.offlineNote') : t('pocketWifiSync.closeNote')}</p>
      <div className="new-item-actions">
        <button type="button" className="secondary-button" disabled={wifi.cancelling} onClick={() => void call({ action: 'wifi-cancel' })}>
          {wifi.cancelling ? t('pocketWifiSync.cancelling') : t('pocketWifiSync.cancel')}
        </button>
      </div>
    </>
  );
}

// wifiPhaseText says what the WiFi copy does right now.
function wifiPhaseText(w: PocketWifiState, t: TFunction): string {
  switch (w.phase) {
    case 'listing':
      return t('pocketWifiSync.phases.listing');
    case 'checking':
      return t('pocketWifiSync.phases.checking');
    case 'wifi-starting':
      return t('pocketWifiSync.phases.wifiStarting', { count: w.total });
    case 'downloading':
      return t('pocketWifiSync.phases.downloading', { current: w.current, total: w.total });
    case 'wifi-restarting':
      return t('pocketWifiSync.phases.wifiRestarting');
    case 'reconnecting':
      return t('pocketWifiSync.phases.reconnecting');
    case 'uploading':
      return t('pocketWifiSync.phases.uploading', { current: w.current, total: w.total });
    default:
      return t('pocketWifiSync.phases.connecting');
  }
}
