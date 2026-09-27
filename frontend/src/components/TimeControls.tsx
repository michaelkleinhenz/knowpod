import { FormEvent, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { useNow } from '../hooks/useNow';
import { errorText } from '../lib/errors';
import { FOCUS_MINUTES, formatClockDuration, formatMinutes, formatSeconds, parseEstimate } from '../lib/timer';
import { ClockIcon, FocusIcon, PlayIcon, StopIcon, StopwatchIcon } from './Icons';

const ESTIMATES = [15, 30, 60, 120, 240];

// EstimatePicker is a popover to set how long a task is expected to take: typed ("1h30") or
// picked.
function EstimatePicker({ rec, save, onClose }: { rec: Recording; save: (minutes: number) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState(rec.estimate ? String(rec.estimate) : '');
  const minutes = parseEstimate(text);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (!(e.target as Element).closest?.('.estimate-add')) onClose();
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (minutes !== null) void save(minutes).then(onClose);
  };

  return (
    <div className="label-popover estimate-popover" role="dialog" aria-label={t('time.estimateTitle')}>
      <form onSubmit={submit} className="estimate-form">
        <label>
          <span className="sr-only">{t('time.estimate')}</span>
          <input autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder={t('time.estimatePlaceholder')} aria-invalid={minutes === null} />
        </label>
        <button type="submit" className="small-button" disabled={minutes === null}>
          {t('common.save')}
        </button>
      </form>
      {text.trim() && <p className={minutes === null ? 'error' : 'muted'}>{minutes === null ? t('time.estimateInvalid') : minutes > 0 ? formatMinutes(minutes) : t('time.noEstimate')}</p>}
      <div className="task-quick">
        {ESTIMATES.map((m) => (
          <button key={m} type="button" className={`small-button${rec.estimate === m ? ' active' : ''}`} onClick={() => void save(m).then(onClose)}>
            {formatMinutes(m)}
          </button>
        ))}
        {!!rec.estimate && (
          <button type="button" className="small-button" onClick={() => void save(0).then(onClose)}>
            {t('time.noEstimate')}
          </button>
        )}
      </div>
    </div>
  );
}

// TimeControls shows a note's estimate and the time logged on it, and starts and stops its
// timer: counting up, or as a focus session (Pomodoro) that stops by itself.
export function TimeControls({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const { timer, startTimer, stopTimer } = useNotes();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const running = timer?.noteId === rec.id ? timer : null;
  const now = useNow(running ? 1000 : null);

  // Once the timer on this note stops (here, in the list, or at the end of a focus session),
  // the note's logged time grew: load it again.
  const wasRunning = useRef(!!running);
  useEffect(() => {
    if (wasRunning.current && !running) {
      api.recording(rec.id).then(setRec, () => undefined);
    }
    wasRunning.current = !!running;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  };
  const saveEstimate = (minutes: number) => act(async () => setRec(await api.setNoteEstimate(rec.id, minutes)));

  const elapsed = running ? (now - Date.parse(running.start)) / 1000 : 0;
  const tracked = (rec.trackedSeconds ?? 0) + elapsed;
  const left = running?.until ? (Date.parse(running.until) - now) / 1000 : null;
  const over = !!rec.estimate && tracked > rec.estimate * 60;

  return (
    <div className="time-controls">
      <div className="label-add estimate-add">
        <button type="button" className={`label-add-button${rec.estimate ? ' has-due' : ''}`} aria-expanded={open} onClick={() => setOpen(!open)} title={t('time.estimate')}>
          <ClockIcon size={13} /> {rec.estimate ? formatMinutes(rec.estimate) : t('time.setEstimate')}
        </button>
        {open && <EstimatePicker rec={rec} save={saveEstimate} onClose={() => setOpen(false)} />}
      </div>
      {(tracked >= 60 || running) && (
        <span className={`time-tracked${over ? ' over' : ''}`} title={t(over ? 'time.overEstimate' : 'time.trackedTitle')}>
          {t('time.logged', { time: formatSeconds(tracked) })}
        </span>
      )}
      {!rec.deletedAt &&
        (running ? (
          <button type="button" className="pill-button timer-running" disabled={busy} onClick={() => void act(stopTimer)} title={t('timer.stop')}>
            <StopIcon size={12} />
            <span className="timer-clock">{left !== null ? t('timer.left', { time: formatClockDuration(left) }) : formatClockDuration(elapsed)}</span>
          </button>
        ) : (
          <span className="timer-buttons">
            <button type="button" className="icon-button small" disabled={busy} onClick={() => void act(() => startTimer(rec.id))} title={t('timer.start')} aria-label={t('timer.start')}>
              <PlayIcon />
            </button>
            <button
              type="button"
              className="icon-button small"
              disabled={busy}
              onClick={() => void act(() => startTimer(rec.id, FOCUS_MINUTES))}
              title={t('timer.focus', { count: FOCUS_MINUTES })}
              aria-label={t('timer.focus', { count: FOCUS_MINUTES })}
            >
              <FocusIcon />
            </button>
          </span>
        ))}
      {error && <p className="error">{error}</p>}
    </div>
  );
}

// TimerBar shows the running timer above the notes list, with the note it runs on, so it
// can be stopped from anywhere.
export function TimerBar() {
  const { t } = useTranslation();
  const { timer, stopTimer } = useNotes();
  const now = useNow(timer ? 1000 : null);
  const [error, setError] = useState<string | null>(null);
  if (!timer) return null;
  const elapsed = (now - Date.parse(timer.start)) / 1000;
  const left = timer.until ? (Date.parse(timer.until) - now) / 1000 : null;
  const stop = async () => {
    setError(null);
    try {
      await stopTimer();
    } catch (err) {
      setError(errorText(err, t));
    }
  };
  return (
    <div className={`timer-bar${timer.until ? ' focus' : ''}`} role="status">
      {timer.until ? <FocusIcon size={16} /> : <StopwatchIcon size={16} />}
      <Link to={`/conversations/${timer.noteId}`} className="timer-bar-title">
        {timer.noteTitle}
      </Link>
      <span className="timer-clock">{left !== null ? t('timer.left', { time: formatClockDuration(left) }) : formatClockDuration(elapsed)}</span>
      <button type="button" className="icon-button small" onClick={() => void stop()} title={t('timer.stop')} aria-label={t('timer.stop')}>
        <StopIcon />
      </button>
      {error && <p className="error">{error}</p>}
    </div>
  );
}
