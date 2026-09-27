import { FormEvent, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { useTaskParse } from '../lib/useTaskParse';
import { formatClockTime, formatDue, formatRepeat, groupTasks } from '../lib/tasks';
import { isTask } from '../lib/labels';
import { formatDate, when } from '../lib/recordings';
import { CalendarIcon, NewNoteIcon } from './Icons';
import { NoteTreeRows, useNoteTree } from './NoteTree';
import { PriorityFlag } from './TaskControls';

// QuickAdd creates a task from one line, reading its date, time, repeat rule and priority
// from the words ("Call Anna tomorrow 3pm p1"); a preview shows what was understood.
function QuickAdd() {
  const { t } = useTranslation();
  const { upsert } = useNotes();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { parsed: typed, onKeyDown } = useTaskParse(text);
  const parsed = typed!;
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
        onKeyDown={onKeyDown}
        placeholder={t('tasks.quickAddPlaceholder')}
        aria-label={t('tasks.quickAdd')}
        maxLength={300}
        enterKeyHint="done"
      />
      <button type="submit" className="quick-add-button" disabled={busy || !title} title={t('tasks.add')} aria-label={t('tasks.add')}>
        <NewNoteIcon />
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
// date). Tasks with sub-notes open like in the folder view; a sub-task is listed under its
// task (when that is listed too) instead of on its own.
export function TasksView({
  notes,
  activeId,
  aiReady,
  onSetDone,
  onNewSub,
}: {
  notes: Recording[];
  activeId?: string;
  aiReady: boolean;
  onSetDone: (r: Recording, done: boolean) => void;
  onNewSub?: (parent: Recording) => void;
}) {
  const { t } = useTranslation();
  const { recordings } = useNotes();
  const tree = useNoteTree(recordings ?? notes, notes);
  const groups = useMemo(() => {
    const byId = new Map((recordings ?? notes).map((r) => [r.id, r]));
    const listed = new Set(notes.filter((r) => isTask(r) && !r.done).map((r) => r.id));
    // underTask reports whether a listed task is above r (at any depth).
    const underTask = (r: Recording) => {
      let n = 0;
      for (let p = r.parentId ? byId.get(r.parentId) : undefined; p && n < 32; p = p.parentId ? byId.get(p.parentId) : undefined, n++) {
        if (listed.has(p.id)) return true;
      }
      return false;
    };
    return groupTasks(notes.filter((r) => !underTask(r)));
  }, [notes, recordings]);
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
            <NoteTreeRows
              list={g.items}
              tree={tree}
              searching={false}
              activeId={activeId}
              aiReady={aiReady}
              meta={(r, depth) =>
                depth > 0
                  ? formatDate(when(r))
                  : !r.due
                    ? ''
                    : g.key === 'overdue' || g.key === 'later'
                      ? formatDate(`${r.due.date}T12:00:00`, { month: 'short', day: 'numeric' })
                      : r.due.time
                        ? formatClockTime(r.due.time)
                        : ''
              }
              taskDate={false}
              onSetDone={onSetDone}
              onNewSub={onNewSub}
            />
          </ul>
        </div>
      ))}
    </div>
  );
}
