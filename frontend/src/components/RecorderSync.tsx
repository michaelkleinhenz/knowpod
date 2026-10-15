import { useCallback, useEffect, useRef, useState } from 'react';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { api } from '../api/client';
import { airecBluetooth, AirecRecorder, AirecRecording, AirecResult, appContext } from '../lib/desktop';
import { errorText } from '../lib/errors';
import { formatBytes, formatDate, formatDuration } from '../lib/recordings';
import { DownloadIcon, TrashIcon } from './Icons';
import { PocketSyncPane, pocketUsbSyncAvailable } from './PocketUsbSync';

// A recorder type the Recorder Sync dialog can talk to. The tabs are shown in this order; a type
// whose app bridge is missing (e.g. AIREC outside the desktop app) is left out.
type RecorderType = 'pocket' | 'airec';

function availableTypes(): RecorderType[] {
  const types: RecorderType[] = [];
  if (pocketUsbSyncAvailable()) types.push('pocket');
  if (airecBluetooth()) types.push('airec');
  return types;
}

// recorderSyncAvailable says whether the Recorder Sync button is shown: whenever at least one
// supported recorder type can be reached (the desktop and Android apps).
export const recorderSyncAvailable = () => availableTypes().length > 0;

const tabStorageKey = 'knowpod.recorderSyncTab';

function storedTab(types: RecorderType[]): RecorderType {
  try {
    const value = window.localStorage.getItem(tabStorageKey) as RecorderType | null;
    if (value && types.includes(value)) return value;
  } catch {
    // not kept: fine
  }
  return types[0];
}

// RecorderSyncDialog copies recordings from a recorder into knowpod. It has a tab per supported
// recorder type: the Pocket (USB/WiFi, see PocketSyncPane) and the AIREC (Bluetooth, this file).
export function RecorderSyncDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const [types] = useState(availableTypes);
  const [tab, setTab] = useState(() => storedTab(types));

  const pick = (type: RecorderType) => {
    setTab(type);
    try {
      window.localStorage.setItem(tabStorageKey, type);
    } catch {
      // not kept: fine
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const heading = t('recorderSync.title', { context: appContext() });
  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="new-item-dialog pocket-sync-dialog" role="dialog" aria-modal="true" aria-label={heading}>
        <h2>{heading}</h2>
        {types.length > 1 && (
          <div className="segmented" role="tablist" aria-label={t('recorderSync.type')}>
            {types.map((type) => (
              <button
                key={type}
                type="button"
                role="tab"
                aria-selected={tab === type}
                className={tab === type ? 'active' : undefined}
                onClick={() => pick(type)}
              >
                {t(`recorderSync.types.${type}`)}
              </button>
            ))}
          </div>
        )}
        {/* Each pane holds a Bluetooth connection and cleans it up when it unmounts, so only the
            visible tab's pane is mounted. */}
        {tab === 'pocket' ? <PocketSyncPane onClose={onClose} /> : <AirecPane />}
        <div className="new-item-actions">
          <button type="button" className="secondary-button" onClick={onClose}>
            {t('recorderSync.close')}
          </button>
        </div>
      </div>
    </div>
  );
}

type AirecCall = (request: Parameters<NonNullable<ReturnType<typeof airecBluetooth>>>[0]) => Promise<AirecResult>;

// AirecPane connects to an AIREC recorder over Bluetooth (desktop/src/airec-bluetooth.js), shows
// its details and recordings, and lets the user import a recording into knowpod or delete it from
// the recorder. The recorder has no pairing, so opening the tab connects straight away.
function AirecPane() {
  const { t } = useTranslation();
  const [call] = useState<AirecCall>(() => airecBluetooth()! as AirecCall);
  const [connecting, setConnecting] = useState(true);
  const [recorder, setRecorder] = useState<AirecRecorder | null>(null);
  const [recordings, setRecordings] = useState<AirecRecording[]>([]);
  const [error, setError] = useState<string | null>(null);
  // The recording a download or delete is running on, so its row shows a spinner and the others
  // are disabled (one Bluetooth operation at a time).
  const [busyName, setBusyName] = useState<string | null>(null);
  // Names imported into knowpod this session, so their rows show "imported".
  const [imported, setImported] = useState<Set<string>>(new Set());
  const mounted = useRef(true);

  const connect = useCallback(async () => {
    setConnecting(true);
    setError(null);
    try {
      const r = await call({ action: 'connect' });
      if (!mounted.current) return;
      if (r.ok) {
        setRecorder(r.recorder ?? null);
        setRecordings(r.recordings ?? []);
      } else {
        setError(airecErrorText(r.error, r.message, t));
      }
    } catch (err) {
      if (mounted.current) setError(errorText(err, t));
    } finally {
      if (mounted.current) setConnecting(false);
    }
  }, [call, t]);

  useEffect(() => {
    mounted.current = true;
    void connect();
    return () => {
      mounted.current = false;
      void call({ action: 'disconnect' }).catch(() => undefined);
    };
  }, [call, connect]);

  async function download(rec: AirecRecording) {
    setBusyName(rec.name);
    setError(null);
    try {
      const r = await call({ action: 'download', name: rec.name });
      if (!mounted.current) return;
      if (!r.ok || !r.oggBase64) {
        setError(airecErrorText(r.error, r.message, t));
        return;
      }
      const file = new File([base64ToBlob(r.oggBase64, 'audio/ogg')], `${rec.name}.opus`, { type: 'audio/ogg' });
      await api.uploadRecording(file, () => undefined, {
        recorder: true,
        recordedAt: r.recordedAt ? new Date(r.recordedAt) : rec.recordedAt ? new Date(rec.recordedAt) : undefined,
      });
      if (mounted.current) setImported((prev) => new Set(prev).add(rec.name));
    } catch (err) {
      if (mounted.current) setError(errorText(err, t));
    } finally {
      if (mounted.current) setBusyName(null);
    }
  }

  async function remove(rec: AirecRecording) {
    if (!window.confirm(t('recorderSync.airec.confirmDelete', { date: recordingLabel(rec, t) }))) return;
    setBusyName(rec.name);
    setError(null);
    try {
      const r = await call({ action: 'delete', name: rec.name });
      if (!mounted.current) return;
      if (r.ok) setRecordings(r.recordings ?? recordings.filter((x) => x.name !== rec.name));
      else setError(airecErrorText(r.error, r.message, t));
    } catch (err) {
      if (mounted.current) setError(errorText(err, t));
    } finally {
      if (mounted.current) setBusyName(null);
    }
  }

  if (connecting) return <p className="muted">{t('recorderSync.airec.connecting')}</p>;
  if (error && !recorder) {
    return (
      <>
        <p className="error">{error}</p>
        <div className="new-item-actions">
          <button type="button" className="primary-button" onClick={() => void connect()}>
            {t('recorderSync.airec.retry')}
          </button>
        </div>
      </>
    );
  }

  const busy = busyName !== null;
  return (
    <div className="airec-pane">
      {recorder && <AirecDetails recorder={recorder} />}
      {error && <p className="error">{error}</p>}
      {recordings.length === 0 ? (
        <p className="muted">{t('recorderSync.airec.empty')}</p>
      ) : (
        <ul className="airec-recordings">
          {recordings.map((rec) => {
            const done = imported.has(rec.name);
            const working = busyName === rec.name;
            return (
              <li key={rec.name} className="airec-recording">
                <div className="airec-recording-main">
                  <span className="airec-recording-when">{recordingLabel(rec, t)}</span>
                  <span className="muted airec-recording-meta">
                    {formatDuration(rec.seconds * 1000)} · {formatBytes(rec.size)}
                    {done && ` · ${t('recorderSync.airec.imported')}`}
                  </span>
                </div>
                <div className="airec-recording-actions">
                  <button
                    type="button"
                    className="icon-button"
                    disabled={busy}
                    title={t('recorderSync.airec.download')}
                    aria-label={t('recorderSync.airec.download')}
                    onClick={() => void download(rec)}
                  >
                    {working ? <span className="ai-spinner" aria-hidden="true" /> : <DownloadIcon />}
                  </button>
                  <button
                    type="button"
                    className="icon-button danger"
                    disabled={busy}
                    title={t('recorderSync.airec.delete')}
                    aria-label={t('recorderSync.airec.delete')}
                    onClick={() => void remove(rec)}
                  >
                    <TrashIcon />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function AirecDetails({ recorder }: { recorder: AirecRecorder }) {
  const { t } = useTranslation();
  const facts: string[] = [];
  if (recorder.battery != null) facts.push(t('recorderSync.airec.battery', { percent: recorder.battery }));
  if (recorder.storageFreeMb != null && recorder.storageTotalMb != null) {
    facts.push(t('recorderSync.airec.storage', { free: formatMb(recorder.storageFreeMb), total: formatMb(recorder.storageTotalMb) }));
  }
  if (recorder.firmware) facts.push(t('recorderSync.airec.firmware', { version: recorder.firmware }));
  return (
    <div className="airec-details">
      <div className="airec-details-name">{recorder.name || 'AIREC'}</div>
      {facts.length > 0 && <div className="muted airec-details-facts">{facts.join(' · ')}</div>}
      {recorder.serial && <div className="muted airec-details-serial">{t('recorderSync.airec.serial', { serial: recorder.serial })}</div>}
    </div>
  );
}

function recordingLabel(rec: AirecRecording, t: TFunction): string {
  return rec.recordedAt ? formatDate(rec.recordedAt, { dateStyle: 'medium', timeStyle: 'short' }) : rec.name;
}

// base64ToBlob turns the download's base64 Ogg Opus into a Blob to upload.
function base64ToBlob(base64: string, type: string): Blob {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return new Blob([bytes], { type });
}

// storage is reported in MB (a guess from the capture); show it in GB when large.
function formatMb(mb: number): string {
  return mb >= 1024 ? `${(mb / 1024).toLocaleString(undefined, { maximumFractionDigits: 1 })} GB` : `${mb} MB`;
}

const airecErrorKeys = ['not-found', 'unsupported', 'auth', 'busy', 'timeout', 'disconnected', 'recorder', 'transfer'];

function airecErrorText(error: string | undefined, message: string | undefined, t: TFunction): string {
  if (error && airecErrorKeys.includes(error)) return t(`recorderSync.airec.errors.${error}`);
  return t('recorderSync.airec.errors.failed', { detail: message || error || '' });
}
