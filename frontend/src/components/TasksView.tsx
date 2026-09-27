import { FormEvent, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { parseTask } from '../lib/dateParse';
import { formatClockTime, formatDue, formatRepeat, groupTasks } from '../lib/tasks';
import { formatDate } from '../lib/recordings';
import { CalendarIcon } from './Icons';
import { NoteRow } from './NoteRow';
import { PriorityFlag } from './TaskControls';

// QuickAdd creates a task from one line, reading its date, time, repeat rule and priority
// from the words ("Call Anna tomorrow 3pm p1"); a preview shows what was understood.
function QuickAdd() {
  const { t } = useTranslation();
  const { upsert } = useNotes();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const parsed = useMemo(() => parseTask(text), [text]);
  const title = parsed.title || text.trim();

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!title) return;
    setBusy(true);
    setError(null);
    try {
      const due = parsed.due ? { remind: 0, ...parsed.due } : undefined;
      upsert(await api.createTextNote(title, '', undefined, { task: true, due, priority: parsed.priority }));
      setText('');
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="quick-add" onSubmit={submit}>
      <input
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={t('tasks.quickAddPlaceholder')}
        aria-label={t('tasks.quickAdd')}
        maxLength={300}
        enterKeyHint="done"
      />
      <button type="submit" className="small-button" disabled={busy || !title}>
        {t('tasks.add')}
      </button>
      {text.trim() && (parsed.due || parsed.priority) && (
        <p className="quick-add-preview" aria-live="polite">
          <span className="quick-add-title">{title}</span>
          {parsed.due && (
            <span className="due-chip small">
              <CalendarIcon size={12} />
              {formatDue(parsed.due)}
              {parsed.due.repeat && ` · ${formatRepeat(parsed.due.repeat)}`}
            </span>
          )}
          <PriorityFlag priority={parsed.priority} />
        </p>
      )}
      {error && <p className="error">{error}</p>}
    </form>
  );
}

// TasksView lists the open tasks by when they are due: overdue, today, the next days,
// later, and without a date. Checking one off hides it (a recurring one moves to its next
// date).
export function TasksView({ notes, activeId, aiReady, onSetDone }: { notes: Recording[]; activeId?: string; aiReady: boolean; onSetDone: (r: Recording, done: boolean) => void }) {
  const { t } = useTranslation();
  const groups = useMemo(() => groupTasks(notes), [notes]);
  return (
    <div className="tasks-view">
      <QuickAdd />
      {groups.length === 0 && <p className="muted empty">{t('tasks.empty')}</p>}
      {groups.map((g) => (
        <div key={g.key} className={`day-group${g.overdue ? ' overdue-group' : ''}`}>
          <h2 className="day-heading">
            {g.label} {g.date && <span>{g.date}</span>}
          </h2>
          <ul className="conversation-list">
            {g.items.map((r) => (
              <NoteRow
                key={r.id}
                rec={r}
                active={r.id === activeId}
                aiReady={aiReady}
                meta={
                  !r.due
                    ? ''
                    : g.key === 'overdue' || g.key === 'later'
                      ? formatDate(`${r.due.date}T12:00:00`, { month: 'short', day: 'numeric' })
                      : r.due.time
                        ? formatClockTime(r.due.time)
                        : ''
                }
                taskDate={false}
                onSetDone={onSetDone}
              />
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}
