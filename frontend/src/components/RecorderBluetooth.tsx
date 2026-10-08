import { FormEvent, useCallback, useEffect, useState } from 'react';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { appContext, recorderBluetooth, RecorderBluetoothRequest, RecorderBluetoothState } from '../lib/desktop';

// errorKeys are the explained failures of the app's recorder Bluetooth call.
const errorKeys = [
  'not-found',
  'unsupported',
  'permission',
  'auth',
  'busy',
  'timeout',
  'disconnected',
  'no-token',
  'other-server',
  'token',
  'no-server',
  'invalid-pin',
];

// Phases in which the app is busy with the recorder: the state is asked for more often then.
const activePhases = ['searching', 'connecting', 'pin', 'listing', 'preparing', 'uploading'];

function errorText(s: RecorderBluetoothState, t: TFunction): string {
  return s.error && errorKeys.includes(s.error)
    ? t(`recorderBluetooth.errors.${s.error}`, { context: appContext(), detail: s.message || '' })
    : t('recorderBluetooth.errors.failed', { detail: s.message || s.error || '' });
}

// RecorderBluetooth pairs the app with the knowpod recorder (the ESP32 gadget) and shows how
// copying from it goes: where the recorder has no Wi-Fi, it hands its recordings to the app over
// Bluetooth, and the app uploads them with the recorder's device token (docs/ble-transfer.md).
// Only in the desktop and mobile apps.
export function RecorderBluetooth() {
  const { t } = useTranslation();
  // Looked up once: the effects below must not run again on every render.
  const [call] = useState(() => recorderBluetooth());
  const [state, setState] = useState<RecorderBluetoothState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pin, setPin] = useState('');

  const refresh = useCallback(async () => {
    if (!call) return;
    try {
      setState(await call({ action: 'state' }));
    } catch {
      // the app went away; keep what is shown
    }
  }, [call]);

  const active = !!state && (!!state.busy || activePhases.includes(state.phase || ''));
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), active ? 1000 : 5000);
    return () => clearInterval(timer);
  }, [refresh, active]);

  if (!call) return null;
  if (!state) return <p className="muted">{t('common.loading')}</p>;

  async function run(request: RecorderBluetoothRequest) {
    setError(null);
    try {
      const r = await call!(request);
      if (!r.ok) setError(errorText(r, t));
    } catch (err) {
      setError(t('recorderBluetooth.errors.failed', { detail: err instanceof Error ? err.message : String(err) }));
    }
    await refresh();
  }

  function handlePin(e: FormEvent) {
    e.preventDefault();
    if (!/^\d{6}$/.test(pin.trim())) {
      setError(t('recorderBluetooth.errors.invalid-pin'));
      return;
    }
    void run({ action: 'pin', pin: pin.trim() });
    setPin('');
  }

  function forget() {
    if (!window.confirm(t('recorderBluetooth.forgetConfirm', { name: state?.name, context: appContext() }))) return;
    void run({ action: 'forget' });
  }

  const context = appContext();
  const phase = state.phase || '';
  const percent = state.totalBytes ? Math.floor((100 * (state.bytes || 0)) / state.totalBytes) : 0;
  let status = '';
  switch (phase) {
    case 'searching':
    case 'connecting':
    case 'listing':
      status = t(`recorderBluetooth.phases.${phase}`);
      break;
    case 'pin':
      status = state.systemPin ? t('recorderBluetooth.phases.systemPin') : t('recorderBluetooth.phases.pin');
      break;
    case 'preparing':
    case 'uploading':
      status = t(`recorderBluetooth.phases.${phase}`, { current: state.current, total: state.total, percent });
      break;
    case 'done':
      status = state.copied ? t('recorderBluetooth.phases.done', { count: state.copied }) : t('recorderBluetooth.phases.doneNone');
      if (state.failed) status += ` ${t('recorderBluetooth.phases.someFailed', { count: state.failed, detail: state.message })}`;
      break;
  }

  return (
    <>
      <h3 className="subheading">{t('recorderBluetooth.title')}</h3>
      <p className="muted">{t('recorderBluetooth.intro', { context })}</p>
      {state.paired ? (
        <>
          <p>{t('recorderBluetooth.paired', { name: state.name })}</p>
          <label className="checkbox">
            <input
              type="checkbox"
              checked={!!state.enabled}
              onChange={(e) => void run({ action: 'enable', enabled: e.target.checked })}
            />
            {t('recorderBluetooth.enabled')}
          </label>
        </>
      ) : (
        <p className="muted">{t('recorderBluetooth.pairSteps')}</p>
      )}

      {status && <p className={phase === 'done' && state.failed ? 'error' : 'muted'}>{status}</p>}
      {phase === 'connecting' && !state.paired && <p className="muted">{t('recorderBluetooth.pinHint')}</p>}
      {phase === 'failed' && <p className="error">{errorText(state, t)}</p>}
      {error && <p className="error">{error}</p>}

      {phase === 'pin' && !state.systemPin && (
        <form onSubmit={handlePin} className="inline-form">
          <label className="sr-only" htmlFor="recorder-pin">
            {t('recorderBluetooth.pin')}
          </label>
          <input
            id="recorder-pin"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            placeholder={t('recorderBluetooth.pin')}
            value={pin}
            onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))}
          />
          <button type="submit" disabled={pin.length !== 6}>
            {t('recorderBluetooth.pinSubmit')}
          </button>
        </form>
      )}

      {phase === 'choose' && !!state.devices?.length && (
        <>
          <p className="muted">{t('recorderBluetooth.choose')}</p>
          <div className="button-row">
            {state.devices.map((name) => (
              <button key={name} type="button" className="secondary-button" disabled={state.busy} onClick={() => void run({ action: 'pair', name })}>
                {name}
              </button>
            ))}
          </div>
        </>
      )}

      <div className="button-row">
        {state.busy ? (
          <button type="button" className="secondary-button" onClick={() => void run({ action: 'cancel' })}>
            {t('common.cancel')}
          </button>
        ) : (
          <>
            {state.paired && state.enabled && (
              <button type="button" onClick={() => void run({ action: 'sync' })}>
                {t('recorderBluetooth.syncNow')}
              </button>
            )}
            <button type="button" className={state.paired ? 'secondary-button' : undefined} onClick={() => void run({ action: 'pair' })}>
              {state.paired ? t('recorderBluetooth.pairAgain') : t('recorderBluetooth.pair')}
            </button>
            {state.paired && (
              <button type="button" className="secondary-button danger" onClick={forget}>
                {t('recorderBluetooth.forget')}
              </button>
            )}
          </>
        )}
      </div>
    </>
  );
}
