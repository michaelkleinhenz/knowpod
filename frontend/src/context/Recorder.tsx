import { createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { CheckIcon, ChevronDownIcon, FlagIcon, MicIcon, PauseIcon, PlayIcon, StopIcon, TrashIcon } from '../components/Icons';
import { errorText } from '../lib/errors';
import { formatBytes, formatClock, formatTime } from '../lib/recordings';
import { RecorderUnsupportedError, SAMPLE_RATE, WavRecorder } from '../lib/wavRecorder';

// MAX_MS is the longest voice memo; the recording is saved when it is reached.
const MAX_MS = 2 * 60 * 60 * 1000;

type Phase = 'idle' | 'starting' | 'recording' | 'uploading' | 'done' | 'error';

interface RecorderState {
  // start starts recording a voice memo (asking for the microphone first).
  start: () => void;
  active: boolean;
  // recording says a memo is being recorded; expand shows the phone's recording screen again.
  recording: boolean;
  expand: () => void;
}

const RecorderContext = createContext<RecorderState>({ start: () => undefined, active: false, recording: false, expand: () => undefined });

export const useRecorder = () => useContext(RecorderContext);

// iOS suspends a page while the phone is locked, so a memo recorded there stops until the
// app is opened again; Android goes on recording.
const IOS = /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);

type WakeLock = { release: () => Promise<void> };

async function requestWakeLock(): Promise<WakeLock | null> {
  try {
    const nav = navigator as Navigator & { wakeLock?: { request: (type: 'screen') => Promise<WakeLock> } };
    return (await nav.wakeLock?.request('screen')) ?? null;
  } catch {
    // The screen may turn off; see IOS above.
    return null;
  }
}

// memoName names a voice memo after when it was started; the AI summary names it later.
function memoName(prefix: string, d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${prefix} ${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}.${pad(d.getMinutes())}.wav`;
}

// RecorderProvider records voice memos in the app. It lives in the app frame, so a memo keeps
// recording while the user moves between pages; a bar floating at the bottom shows it.
// Highlights marked while recording go along with the memo, like a recorder's button.
// On a phone the recording fills the screen with big buttons (the bar shows when it is
// minimized).
export function RecorderProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<string | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [level, setLevel] = useState(0);
  const [paused, setPaused] = useState(false);
  const [marks, setMarks] = useState<number[]>([]);
  const [progress, setProgress] = useState(0);
  const [savedId, setSavedId] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(true);
  const [interrupted, setInterrupted] = useState(false);
  const [flash, setFlash] = useState(0);
  const recorder = useRef<WavRecorder | null>(null);
  const startedAt = useRef<Date>(new Date());
  const wakeLock = useRef<WakeLock | null>(null);
  const failedUpload = useRef<{ file: File; highlights: number[] } | null>(null);

  const releaseWakeLock = () => {
    void wakeLock.current?.release().catch(() => undefined);
    wakeLock.current = null;
  };

  const upload = useCallback(
    async (file: File, highlights: number[]) => {
      setPhase('uploading');
      setProgress(0);
      setError(null);
      try {
        const rec = await api.uploadRecording(file, setProgress, { recorder: true, recordedAt: startedAt.current, highlights });
        failedUpload.current = null;
        setSavedId(rec.id);
        setPhase('done');
        setTimeout(() => setPhase((p) => (p === 'done' ? 'idle' : p)), 6000);
      } catch (err) {
        failedUpload.current = { file, highlights };
        setError(errorText(err, t));
        setPhase('error');
      }
    },
    [t],
  );

  const save = useCallback(() => {
    const r = recorder.current;
    if (!r) return;
    recorder.current = null;
    releaseWakeLock();
    const blob = r.stop();
    const file = new File([blob], memoName(t('recorder.memoName'), startedAt.current), { type: 'audio/wav' });
    void upload(file, marks);
  }, [marks, t, upload]);

  const start = useCallback(async () => {
    if (recorder.current || phase === 'starting' || phase === 'uploading') return;
    setPhase('starting');
    setError(null);
    setMarks([]);
    setElapsed(0);
    setPaused(false);
    setSavedId(null);
    setExpanded(true);
    setInterrupted(false);
    try {
      recorder.current = await WavRecorder.start();
      startedAt.current = new Date();
      setPhase('recording');
      wakeLock.current = await requestWakeLock();
    } catch (err) {
      recorder.current = null;
      setError(
        err instanceof RecorderUnsupportedError
          ? t('recorder.unsupported')
          : err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'SecurityError')
            ? t('recorder.denied')
            : err instanceof DOMException && err.name === 'NotFoundError'
              ? t('recorder.noMicrophone')
              : errorText(err, t),
      );
      setPhase('error');
    }
  }, [phase, t]);

  // The clock and the level meter follow the recording.
  useEffect(() => {
    if (phase !== 'recording') return;
    const timer = setInterval(() => {
      const r = recorder.current;
      if (!r) return;
      setElapsed(r.elapsedMs);
      setLevel(r.level);
      setInterrupted(r.interrupted);
      if (r.elapsedMs >= MAX_MS) save();
    }, 100);
    return () => clearInterval(timer);
  }, [phase, save]);

  // Back from the lock screen or another app: the system may have suspended the recording,
  // and it dropped the wake lock.
  useEffect(() => {
    if (phase !== 'recording') return;
    const onVisible = () => {
      const r = recorder.current;
      if (document.visibilityState !== 'visible' || !r) return;
      void r.revive();
      if (!wakeLock.current) return;
      void requestWakeLock().then((lock) => {
        if (recorder.current === r) wakeLock.current = lock;
        else void lock?.release().catch(() => undefined);
      });
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [phase]);

  // Leaving the app while recording asks first.
  useEffect(() => {
    if (phase !== 'recording' && phase !== 'uploading') return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [phase]);

  function togglePause() {
    const r = recorder.current;
    if (!r) return;
    if (r.isPaused) r.resume();
    else r.pause();
    setPaused(r.isPaused);
  }

  function mark() {
    const r = recorder.current;
    if (!r) return;
    setMarks((m) => [...m, r.elapsedMs]);
    setFlash((n) => n + 1);
    navigator.vibrate?.(40);
  }

  function discard() {
    if ((recorder.current || failedUpload.current) && !window.confirm(t('recorder.discardConfirm'))) return;
    recorder.current?.cancel();
    recorder.current = null;
    failedUpload.current = null;
    releaseWakeLock();
    setPhase('idle');
    setError(null);
  }

  const bar =
    phase === 'idle' ? null : (
      <div className={`recorder-bar ${phase}${paused ? ' paused' : ''}${expanded && (phase === 'starting' || phase === 'recording') ? ' expanded' : ''}`} role="region" aria-label={t('recorder.title')}>
        {phase === 'starting' && <span className="muted">{t('recorder.starting')}</span>}
        {phase === 'recording' && (
          <>
            <button type="button" className="recorder-expand" onClick={() => setExpanded(true)} aria-label={t('recorder.expand')} title={t('recorder.expand')}>
              <span className="recorder-dot" aria-hidden="true" />
              <span className="recorder-time" aria-live="off">
                {formatClock(elapsed)}
              </span>
            </button>
            <span className="recorder-level" aria-hidden="true">
              <span style={{ transform: `scaleX(${paused ? 0 : Math.min(1, level * 1.6)})` }} />
            </span>
            {marks.length > 0 && <span className="recorder-marks">{t('recorder.marks', { count: marks.length })}</span>}
            <button type="button" className="icon-button" title={t('recorder.mark')} aria-label={t('recorder.mark')} onClick={mark} disabled={paused}>
              <FlagIcon />
            </button>
            <button type="button" className="icon-button" title={paused ? t('recorder.resume') : t('recorder.pause')} aria-label={paused ? t('recorder.resume') : t('recorder.pause')} onClick={togglePause}>
              {paused ? <PlayIcon /> : <PauseIcon />}
            </button>
            <button type="button" className="icon-button danger" title={t('recorder.discard')} aria-label={t('recorder.discard')} onClick={discard}>
              <TrashIcon />
            </button>
            <button type="button" className="pill-button recorder-save" onClick={save} disabled={elapsed < 500}>
              <CheckIcon /> <span>{t('recorder.save')}</span>
            </button>
          </>
        )}
        {phase === 'uploading' && (
          <>
            <MicIcon />
            <span>{t('recorder.uploading')}</span>
            <progress max={1} value={progress} />
          </>
        )}
        {phase === 'done' && (
          <>
            <MicIcon />
            <span>{t('recorder.saved')}</span>
            {savedId && (
              <button
                type="button"
                className="pill-button"
                onClick={() => {
                  setPhase('idle');
                  navigate(`/conversations/${savedId}`);
                }}
              >
                {t('recorder.open')}
              </button>
            )}
          </>
        )}
        {phase === 'error' && (
          <>
            <span className="error">{error}</span>
            {failedUpload.current && (
              <button type="button" className="pill-button" onClick={() => failedUpload.current && void upload(failedUpload.current.file, failedUpload.current.highlights)}>
                {t('recorder.retry')}
              </button>
            )}
            <button type="button" className="pill-button" onClick={discard}>
              {failedUpload.current ? t('recorder.discard') : t('conversations.dismiss')}
            </button>
          </>
        )}
      </div>
    );

  // The phone's recording screen; styles hide it on wide screens, which keep the bar.
  const screen =
    expanded && (phase === 'starting' || phase === 'recording') ? (
      <div className={`recorder-screen${paused ? ' paused' : ''}`} role="dialog" aria-modal="true" aria-label={t('recorder.title')}>
        <div className="recorder-screen-top">
          <button type="button" className="recorder-screen-small" onClick={() => setExpanded(false)} aria-label={t('recorder.minimize')} title={t('recorder.minimize')}>
            <ChevronDownIcon size={22} />
          </button>
          <span className="recorder-screen-title">{t('recorder.title')}</span>
          <button type="button" className="recorder-screen-small danger" onClick={discard} aria-label={t('recorder.discard')} title={t('recorder.discard')} disabled={phase !== 'recording'}>
            <TrashIcon />
          </button>
        </div>
        <div className="recorder-screen-main">
          <span className="recorder-screen-state">
            {phase === 'starting' ? (
              t('recorder.starting')
            ) : (
              <>
                <span className="recorder-dot" aria-hidden="true" /> {paused ? t('recorder.paused') : t('recorder.recording')}
              </>
            )}
          </span>
          <span className="recorder-screen-time" aria-live="off">
            {formatClock(elapsed)}
          </span>
          <span className="recorder-screen-level" aria-hidden="true">
            <span style={{ transform: `scaleX(${paused || phase !== 'recording' ? 0 : Math.min(1, level * 1.6)})` }} />
          </span>
          <dl className="recorder-screen-facts">
            <div>
              <dt>{t('recorder.started')}</dt>
              <dd>{phase === 'recording' ? formatTime(startedAt.current) : '—'}</dd>
            </div>
            <div>
              <dt>{t('recorder.highlights')}</dt>
              <dd key={flash} className={flash ? 'recorder-flash' : undefined}>
                {marks.length}
              </dd>
            </div>
            <div>
              <dt>{t('recorder.size')}</dt>
              <dd>{formatBytes((elapsed / 1000) * SAMPLE_RATE * 2)}</dd>
            </div>
          </dl>
          {interrupted ? (
            <p className="recorder-screen-note warning" role="status">
              {t('recorder.interrupted')}
            </p>
          ) : (
            IOS && <p className="recorder-screen-note">{t('recorder.iosLock')}</p>
          )}
        </div>
        <div className="recorder-screen-controls">
          <button type="button" className="recorder-big" onClick={mark} disabled={phase !== 'recording' || paused} aria-label={t('recorder.mark')}>
            <FlagIcon size={30} />
            <span>{t('recorder.highlight')}</span>
          </button>
          <button type="button" className="recorder-big stop" onClick={save} disabled={phase !== 'recording' || elapsed < 500} aria-label={t('recorder.stop')}>
            <StopIcon size={40} />
            <span>{t('recorder.stopShort')}</span>
          </button>
          <button type="button" className="recorder-big" onClick={togglePause} disabled={phase !== 'recording'} aria-label={paused ? t('recorder.resume') : t('recorder.pause')}>
            {paused ? <PlayIcon size={30} /> : <PauseIcon size={30} />}
            <span>{paused ? t('recorder.resume') : t('recorder.pause')}</span>
          </button>
        </div>
      </div>
    ) : null;

  return (
    <RecorderContext.Provider
      value={{
        start: () => void start(),
        active: phase === 'starting' || phase === 'recording' || phase === 'uploading',
        recording: phase === 'starting' || phase === 'recording',
        expand: () => setExpanded(true),
      }}
    >
      {children}
      {bar}
      {screen}
    </RecorderContext.Provider>
  );
}
