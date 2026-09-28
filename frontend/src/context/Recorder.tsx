import { createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { CheckIcon, FlagIcon, MicIcon, PauseIcon, PlayIcon, TrashIcon } from '../components/Icons';
import { errorText } from '../lib/errors';
import { formatClock } from '../lib/recordings';
import { RecorderUnsupportedError, WavRecorder } from '../lib/wavRecorder';

// MAX_MS is the longest voice memo; the recording is saved when it is reached.
const MAX_MS = 2 * 60 * 60 * 1000;

type Phase = 'idle' | 'starting' | 'recording' | 'uploading' | 'done' | 'error';

interface RecorderState {
  // start starts recording a voice memo (asking for the microphone first).
  start: () => void;
  active: boolean;
}

const RecorderContext = createContext<RecorderState>({ start: () => undefined, active: false });

export const useRecorder = () => useContext(RecorderContext);

// memoName names a voice memo after when it was started; the AI summary names it later.
function memoName(prefix: string, d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${prefix} ${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}.${pad(d.getMinutes())}.wav`;
}

// RecorderProvider records voice memos in the app. It lives in the app frame, so a memo keeps
// recording while the user moves between pages; a bar floating at the bottom shows it.
// Highlights marked while recording go along with the memo, like a recorder's button.
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
  const recorder = useRef<WavRecorder | null>(null);
  const startedAt = useRef<Date>(new Date());
  const wakeLock = useRef<{ release: () => Promise<void> } | null>(null);
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
    try {
      recorder.current = await WavRecorder.start();
      startedAt.current = new Date();
      setPhase('recording');
      try {
        const nav = navigator as Navigator & { wakeLock?: { request: (type: 'screen') => Promise<{ release: () => Promise<void> }> } };
        wakeLock.current = (await nav.wakeLock?.request('screen')) ?? null;
      } catch {
        // The screen may turn off; recording goes on while the page stays open.
      }
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
      if (r.elapsedMs >= MAX_MS) save();
    }, 100);
    return () => clearInterval(timer);
  }, [phase, save]);

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
    if (r) setMarks((m) => [...m, r.elapsedMs]);
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
      <div className={`recorder-bar ${phase}${paused ? ' paused' : ''}`} role="region" aria-label={t('recorder.title')}>
        {phase === 'starting' && <span className="muted">{t('recorder.starting')}</span>}
        {phase === 'recording' && (
          <>
            <span className="recorder-dot" aria-hidden="true" />
            <span className="recorder-time" aria-live="off">
              {formatClock(elapsed)}
            </span>
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

  return (
    <RecorderContext.Provider value={{ start: () => void start(), active: phase === 'starting' || phase === 'recording' || phase === 'uploading' }}>
      {children}
      {bar}
    </RecorderContext.Provider>
  );
}
