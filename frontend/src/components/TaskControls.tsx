import { KeyboardEvent, MouseEvent as ReactMouseEvent, useEffect, useMemo, useState } from 'react';
import i18n from 'i18next';
import { useTranslation } from 'react-i18next';
import { api, Due, Priority, Recording, Repeat } from '../api/client';
import { errorText } from '../lib/errors';
import { isoDate, parseTask } from '../lib/dateParse';
import { dueDate, formatDue, formatReminder, formatRepeat, overdue, REMINDERS } from '../lib/tasks';
import { formatMinutes } from '../lib/timer';
import { BellIcon, CalendarIcon, ClockIcon, FlagIcon, RepeatIcon } from './Icons';

export const PRIORITIES: Priority[] = [1, 2, 3];

// PriorityFlag shows a task's priority as a colored flag.
export function PriorityFlag({ priority }: { priority?: Priority }) {
  const { t } = useTranslation();
  if (!priority) return null;
  return (
    <span className={`priority-flag p${priority}`} title={t('tasks.priorityN', { n: priority })} role="img" aria-label={t('tasks.priorityN', { n: priority })}>
      <FlagIcon size={13} filled />
    </span>
  );
}

// DueChip shows when a task is due (red once it is overdue) and whether it repeats.
export function DueChip({ rec, small = false }: { rec: Recording; small?: boolean }) {
  const { t } = useTranslation();
  if (!rec.due) return null;
  const late = overdue(rec);
  return (
    <span className={`due-chip${late ? ' overdue' : ''}${small ? ' small' : ''}`} title={late ? t('tasks.overdue') : undefined}>
      <CalendarIcon size={12} />
      {formatDue(rec.due)}
      {rec.due.repeat && (
        <span className="due-repeat" title={formatRepeat(rec.due.repeat)} aria-label={formatRepeat(rec.due.repeat)}>
          <RepeatIcon />
        </span>
      )}
    </span>
  );
}

// TaskMeta is the priority, estimate and (unless showDue is false) the date of a task, for a
// row in a list.
export function TaskMeta({ rec, showDue = true }: { rec: Recording; showDue?: boolean }) {
  const due = showDue && !rec.done && !!rec.due;
  const estimate = !rec.done && !!rec.estimate;
  if (!due && !rec.priority && !rec.due?.repeat && !estimate) return null;
  return (
    <span className="task-meta">
      <PriorityFlag priority={rec.priority} />
      {estimate && (
        <span className="estimate-chip" title={i18n.t('time.estimate')}>
          <ClockIcon size={11} />
          {formatMinutes(rec.estimate!)}
        </span>
      )}
      {due ? (
        <DueChip rec={rec} small />
      ) : (
        rec.due?.repeat && (
          <span className="due-repeat" title={formatRepeat(rec.due.repeat)} role="img" aria-label={formatRepeat(rec.due.repeat)}>
            <RepeatIcon />
          </span>
        )
      )}
    </span>
  );
}

// REPEATS are the repeat rules offered in the picker; others (typed ones such as "every 2
// weeks") are shown as they are.
const REPEATS: { key: string; repeat?: Repeat }[] = [
  { key: 'none' },
  { key: 'day', repeat: { every: 1, unit: 'day' } },
  { key: 'weekday', repeat: { every: 1, unit: 'weekday' } },
  { key: 'week', repeat: { every: 1, unit: 'week' } },
  { key: 'month', repeat: { every: 1, unit: 'month' } },
  { key: 'year', repeat: { every: 1, unit: 'year' } },
];
const repeatKey = (r?: Repeat) =>
  !r ? 'none' : (REPEATS.find((x) => x.repeat && x.repeat.unit === r.unit && x.repeat.every === r.every && !r.weekdays?.length)?.key ?? 'custom');

// withRepeat sets a picked repeat rule; weekly ones repeat on the date's weekday.
function withRepeat(due: Due, key: string): Due {
  const picked = REPEATS.find((x) => x.key === key)?.repeat;
  const next: Due = { ...due };
  if (!picked) delete next.repeat;
  else next.repeat = picked.unit === 'month' ? { ...picked, monthDay: dueDate(due).getDate() } : { ...picked };
  return next;
}

// openPicker opens a date or time input's native picker on click, as the fields hide its icon.
const openPicker = (e: ReactMouseEvent<HTMLInputElement>) => {
  try {
    e.currentTarget.showPicker?.();
  } catch {
    // Not allowed here (or unsupported); the field still takes typed input.
  }
};

// TaskPicker is a popover that sets a task's date (typed like "tomorrow 3pm" or "every
// monday", or picked), time, repeat rule, reminder and priority. Changes are saved at once.
function TaskPicker({ rec, save, onClose }: { rec: Recording; save: (fn: () => Promise<Recording>) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const typed = useMemo(() => (text.trim() ? parseTask(text) : null), [text]);
  const due = rec.due;

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (!(e.target as Element).closest?.('.task-add')) onClose();
    };
    const onKey = (e: globalThis.KeyboardEvent) => e.key === 'Escape' && onClose();
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  const setDue = (d: Due | null) => save(() => api.setNoteDue(rec.id, d));
  // A new date keeps the time, repeat and reminder already set; a first one reminds.
  const setDate = (date: string) => setDue(due ? { ...due, date } : { date, remind: 0 });
  const today = new Date();
  const quick = [
    { key: 'today', date: isoDate(today) },
    { key: 'tomorrow', date: isoDate(new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1)) },
    { key: 'nextWeek', date: isoDate(new Date(today.getFullYear(), today.getMonth(), today.getDate() + (((8 - today.getDay()) % 7) || 7))) },
  ];

  const applyTyped = () => {
    if (!typed?.due && !typed?.priority) return;
    void save(async () => {
      let r = rec;
      if (typed.due) r = await api.setNoteDue(rec.id, { remind: due?.remind ?? 0, ...typed.due });
      if (typed.priority) r = await api.setNotePriority(rec.id, typed.priority);
      return r;
    }).then(() => setText(''));
  };
  const onTypedKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      applyTyped();
    }
  };

  return (
    <div className="label-popover task-popover" role="dialog" aria-label={t('tasks.pickerTitle')}>
      <label className="task-typed">
        <span className="sr-only">{t('tasks.typeDate')}</span>
        <input autoFocus placeholder={t('tasks.typeDatePlaceholder')} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={onTypedKey} />
      </label>
      {typed && (
        <button type="button" className="task-typed-preview" disabled={!typed.due && !typed.priority} onClick={applyTyped}>
          {typed.due ? (
            <>
              <CalendarIcon size={12} /> {formatDue(typed.due)}
              {typed.due.repeat && ` · ${formatRepeat(typed.due.repeat)}`}
            </>
          ) : !typed.priority ? (
            t('tasks.notUnderstood')
          ) : null}
          {typed.priority ? <PriorityFlag priority={typed.priority} /> : null}
          {(typed.due || typed.priority) && <kbd>↵</kbd>}
        </button>
      )}

      <div className="task-quick">
        {quick.map((q) => (
          <button key={q.key} type="button" className={`small-button${due?.date === q.date ? ' active' : ''}`} onClick={() => void setDate(q.date)}>
            {t(`tasks.quick.${q.key}`)}
          </button>
        ))}
        {due && (
          <button type="button" className="small-button" onClick={() => void setDue(null)}>
            {t('tasks.quick.none')}
          </button>
        )}
      </div>

      <div className="task-fields">
        <label title={t('tasks.date')}>
          <CalendarIcon size={12} />
          <span className="sr-only">{t('tasks.date')}</span>
          <input type="date" onClick={openPicker} value={due?.date ?? ''} onChange={(e) => e.target.value && void setDate(e.target.value)} />
        </label>
        <label title={t('tasks.time')}>
          <ClockIcon />
          <span className="sr-only">{t('tasks.time')}</span>
          <input
            type="time"
            onClick={openPicker}
            value={due?.time ?? ''}
            disabled={!due}
            onChange={(e) => {
              if (!due) return;
              const next: Due = { ...due, time: e.target.value || undefined };
              if (!e.target.value) delete next.time;
              void setDue(next);
            }}
          />
        </label>
        <label title={t('tasks.repeatLabel')}>
          <RepeatIcon />
          <span className="sr-only">{t('tasks.repeatLabel')}</span>
          <select value={repeatKey(due?.repeat)} disabled={!due} onChange={(e) => due && void setDue(withRepeat(due, e.target.value))}>
            {REPEATS.map((r) => (
              <option key={r.key} value={r.key}>
                {r.repeat ? formatRepeat(r.repeat) : t('tasks.repeat.none')}
              </option>
            ))}
            {repeatKey(due?.repeat) === 'custom' && due?.repeat && <option value="custom">{formatRepeat(due.repeat)}</option>}
          </select>
        </label>
        <label title={t('tasks.reminder')}>
          <BellIcon />
          <span className="sr-only">{t('tasks.reminder')}</span>
          <select
            value={due?.remind === undefined ? 'none' : String(due.remind)}
            disabled={!due}
            onChange={(e) => {
              if (!due) return;
              const next: Due = { ...due };
              if (e.target.value === 'none') delete next.remind;
              else next.remind = Number(e.target.value);
              void setDue(next);
            }}
          >
            <option value="none">{formatReminder(undefined, !due?.time)}</option>
            {REMINDERS.map((m) => (
              <option key={m} value={m}>
                {formatReminder(m, !due?.time)}
              </option>
            ))}
            {due?.remind !== undefined && !REMINDERS.includes(due.remind) && <option value={due.remind}>{formatReminder(due.remind, !due.time)}</option>}
          </select>
        </label>
      </div>

      <div className="task-priorities" role="radiogroup" aria-label={t('tasks.priority')}>
        {[...PRIORITIES, 0 as Priority].map((p) => (
          <button
            key={p}
            type="button"
            role="radio"
            aria-checked={(rec.priority ?? 0) === p}
            className={`priority-option p${p}${(rec.priority ?? 0) === p ? ' selected' : ''}`}
            onClick={() => void save(() => api.setNotePriority(rec.id, p))}
            title={p ? t('tasks.priorityN', { n: p }) : t('tasks.noPriority')}
          >
            <FlagIcon size={14} filled={p > 0} />
            {p ? `P${p}` : t('tasks.noPriorityShort')}
          </button>
        ))}
      </div>
    </div>
  );
}

// TaskControls shows a task's date and priority in the note's header, with the button
// that opens the picker. Any note can be given a date; it then becomes a task.
export function TaskControls({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async (fn: () => Promise<Recording>) => {
    setError(null);
    try {
      setRec(await fn());
    } catch (err) {
      setError(errorText(err, t));
    }
  };
  return (
    <div className="label-add task-add">
      <button type="button" className={`label-add-button task-add-button${rec.due ? ' has-due' : ''}${overdue(rec) ? ' overdue' : ''}`} aria-expanded={open} onClick={() => setOpen(!open)}>
        {rec.due ? (
          <>
            <CalendarIcon size={13} /> {formatDue(rec.due)}
            {rec.due.repeat && <RepeatIcon />}
            {rec.due.remind !== undefined && <BellIcon />}
          </>
        ) : (
          <>
            <CalendarIcon size={13} /> {t('tasks.setDate')}
          </>
        )}
        {rec.priority ? <PriorityFlag priority={rec.priority} /> : null}
      </button>
      {open && <TaskPicker rec={rec} save={save} onClose={() => setOpen(false)} />}
      {error && <p className="error">{error}</p>}
    </div>
  );
}
