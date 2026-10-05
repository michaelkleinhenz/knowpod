import { KeyboardEvent, MouseEvent as ReactMouseEvent, useEffect, useMemo, useState } from 'react';
import i18n from 'i18next';
import { useTranslation } from 'react-i18next';
import { api, Due, Priority, Recording, Repeat, Sharing } from '../api/client';
import { errorText } from '../lib/errors';
import { isoDate, parseTask } from '../lib/dateParse';
import { dueDate, formatDue, formatReminder, formatRepeat, nthWeekdayRepeat, overdue, REMINDERS } from '../lib/tasks';
import { isTask } from '../lib/labels';
import { useNotesIfAny } from '../context/NotesContext';
import { DatePicker } from './DatePicker';
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

// repeatsFor are the picker's rules for a date: with one, also monthly on its weekday
// ("every third Friday"), after the plain monthly one.
const repeatsFor = (due?: Due) => {
  if (!due) return REPEATS;
  const i = REPEATS.findIndex((x) => x.key === 'month') + 1;
  return [...REPEATS.slice(0, i), { key: 'monthNth', repeat: nthWeekdayRepeat(dueDate(due)) }, ...REPEATS.slice(i)];
};
const repeatKey = (due?: Due) => {
  const r = due?.repeat;
  if (!r) return 'none';
  if (r.nth) {
    const m = nthWeekdayRepeat(dueDate(due!));
    return r.every === 1 && r.nth === m.nth && r.weekdays?.[0] === m.weekdays?.[0] ? 'monthNth' : 'custom';
  }
  return REPEATS.find((x) => x.repeat && x.repeat.unit === r.unit && x.repeat.every === r.every && !r.weekdays?.length)?.key ?? 'custom';
};

// withRepeat sets a picked repeat rule; weekly ones repeat on the date's weekday.
function withRepeat(due: Due, key: string): Due {
  const picked = repeatsFor(due).find((x) => x.key === key)?.repeat;
  const next: Due = { ...due };
  if (!picked) delete next.repeat;
  else next.repeat = picked.unit === 'month' && !picked.nth ? { ...picked, monthDay: dueDate(due).getDate() } : { ...picked };
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
// monday", or picked), time, repeat rule and reminder. Changes are saved at once.
function TaskPicker({ rec, save, onClose }: { rec: Recording; save: (fn: () => Promise<Recording>) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const typed = useMemo(() => (text.trim() ? parseTask(text) : null), [text]);
  const due = rec.due;
  // counts are the open tasks on each day, shown as dots in the calendar.
  const recordings = useNotesIfAny()?.recordings;
  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const r of recordings ?? []) if (r.id !== rec.id && r.due && !r.done && !r.deletedAt) c[r.due.date] = (c[r.due.date] ?? 0) + 1;
    return c;
  }, [recordings, rec.id]);

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

      <DatePicker inline value={due?.date ?? isoDate(new Date())} counts={counts} onPick={(d) => void setDate(d)} />

      <div className="task-fields">
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
          <select value={repeatKey(due)} disabled={!due} onChange={(e) => due && void setDue(withRepeat(due, e.target.value))}>
            {repeatsFor(due).map((r) => (
              <option key={r.key} value={r.key}>
                {r.repeat ? formatRepeat(r.repeat) : t('tasks.repeat.none')}
              </option>
            ))}
            {repeatKey(due) === 'custom' && due?.repeat && <option value="custom">{formatRepeat(due.repeat)}</option>}
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
    </div>
  );
}

// TaskControls shows a task's date in the note's header, with the button that opens the
// picker. Any note can be given a date; it then becomes a task.
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
      </button>
      {open && <TaskPicker rec={rec} save={save} onClose={() => setOpen(false)} />}
      {error && <p className="error">{error}</p>}
    </div>
  );
}

// TaskPriority shows a task's priority (P4, the default, when none is set) next to its date,
// with a button that opens P1 to P4 to pick from. Any note can be given a priority; it then
// becomes a task.
export function TaskPriority({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const current = rec.priority ?? 0;

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!(e.target as Element).closest?.('.task-priority')) setOpen(false);
    };
    const onKey = (e: globalThis.KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const pick = async (p: Priority) => {
    setError(null);
    try {
      setRec(await api.setNotePriority(rec.id, p));
      setOpen(false);
    } catch (err) {
      setError(errorText(err, t));
    }
  };
  return (
    <div className="label-add task-priority">
      <button
        type="button"
        className={`label-add-button task-priority-button p${current}${current ? ' has-priority' : ''}`}
        aria-expanded={open}
        aria-label={current ? t('tasks.priorityN', { n: current }) : t('tasks.noPriority')}
        title={current ? t('tasks.priorityN', { n: current }) : t('tasks.noPriority')}
        onClick={() => setOpen(!open)}
      >
        <FlagIcon size={13} filled={current > 0} />
        {current ? `P${current}` : isTask(rec) ? t('tasks.noPriorityShort') : t('tasks.priority')}
      </button>
      {open && (
        <div className="label-popover priority-popover task-priorities" role="radiogroup" aria-label={t('tasks.priority')}>
          {[...PRIORITIES, 0 as Priority].map((p) => (
            <button
              key={p}
              type="button"
              role="radio"
              aria-checked={current === p}
              className={`priority-option p${p}${current === p ? ' selected' : ''}`}
              onClick={() => void pick(p)}
              title={p ? t('tasks.priorityN', { n: p }) : t('tasks.noPriority')}
            >
              <FlagIcon size={14} filled={p > 0} />
              {p ? `P${p}` : t('tasks.noPriorityShort')}
            </button>
          ))}
        </div>
      )}
      {error && <p className="error">{error}</p>}
    </div>
  );
}

// TaskPeople shows who reported a task (who made the note) and lets the user pick who it is
// assigned to among the people who have access to the note.
export function TaskPeople({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const [sharing, setSharing] = useState<Sharing | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api.sharing(rec.id).then(
      (s) => live && setSharing(s),
      () => live && setSharing(null),
    );
    return () => {
      live = false;
    };
  }, [rec.id, rec.shared, rec.version]);

  if (!sharing) return null;
  const people = [sharing.owner, ...sharing.members];
  const name = (u: { email: string; userId: string }) => u.email || u.userId;
  const assignee = rec.assigneeId ?? '';
  async function assign(id: string) {
    setError(null);
    try {
      setRec(await api.setNoteAssignee(rec.id, id));
    } catch (err) {
      setError(errorText(err, t));
    }
  }
  return (
    <dl className="note-facts task-people">
      <dt>{t('tasks.reporter')}</dt>
      <dd>{sharing.reporter ? name(sharing.reporter) : '—'}</dd>
      <dt>
        <label htmlFor="task-assignee">{t('tasks.assignee')}</label>
      </dt>
      <dd>
        <select id="task-assignee" value={assignee} onChange={(e) => void assign(e.target.value)}>
          <option value="">{t('tasks.unassigned')}</option>
          {assignee && !people.some((u) => u.userId === assignee) && <option value={assignee}>{assignee}</option>}
          {people.map((u) => (
            <option key={u.userId} value={u.userId}>
              {name(u)}
            </option>
          ))}
        </select>
        {error && <p className="error">{error}</p>}
      </dd>
    </dl>
  );
}
