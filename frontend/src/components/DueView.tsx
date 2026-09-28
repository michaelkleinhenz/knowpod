import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Recording } from '../api/client';
import { locale } from '../i18n';
import { isoDate } from '../lib/dateParse';
import { compareTasks, dayName, dueDate, formatClockTime } from '../lib/tasks';
import { NoteTree, NoteTreeRows } from './NoteTree';

interface Group {
  key: string;
  label: string;
  date: string;
  items: Recording[];
}

// DueView lists the items that have a due date, open or done, in the order of their due
// date (and time), grouped by day. Sub-notes are listed on their own date too, so the list
// is flat: nothing dated hides in a closed note.
export function DueView({
  notes,
  activeId,
  aiReady,
  onSetDone,
  onNewSub,
  onTrash,
}: {
  notes: Recording[];
  activeId?: string;
  aiReady: boolean;
  onSetDone: (r: Recording, done: boolean) => void;
  onNewSub?: (parent: Recording) => void;
  onTrash?: (r: Recording) => void;
}) {
  const { t } = useTranslation();
  const { tree, groups } = useMemo(() => {
    const dated = notes.filter((r) => r.due).sort(compareTasks);
    const tree: NoteTree = { subs: new Map(), roots: dated, shown: new Map(dated.map((r) => [r.id, 1])) };
    // Days are named like in the timeline: Yesterday, Today, Tomorrow, else the weekday.
    const now = new Date();
    const near = new Set([-1, 0, 1].map((n) => isoDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() + n))));
    const out: Group[] = [];
    for (const r of dated) {
      const key = r.due!.date;
      if (out.length === 0 || out[out.length - 1].key !== key) {
        const d = dueDate(r.due!);
        const year = d.getFullYear() === now.getFullYear() ? undefined : 'numeric';
        const label = near.has(key) ? dayName(d, now) : d.toLocaleDateString(locale(), { weekday: 'short' });
        out.push({ key, label, date: d.toLocaleDateString(locale(), { month: 'long', day: 'numeric', year }), items: [] });
      }
      out[out.length - 1].items.push(r);
    }
    return { tree, groups: out };
  }, [notes]);

  if (groups.length === 0) return <p className="muted empty">{t('due.empty')}</p>;
  return (
    <div className="due-view">
      {groups.map((g) => (
        <div key={g.key} className="day-group">
          <h2 className="day-heading">
            {g.label} <span>{g.date}</span>
          </h2>
          <ul className="conversation-list">
            <NoteTreeRows
              list={g.items}
              tree={tree}
              searching={false}
              activeId={activeId}
              aiReady={aiReady}
              meta={(r) => (r.due?.time ? formatClockTime(r.due.time) : '')}
              taskDate={false}
              onSetDone={onSetDone}
              onNewSub={onNewSub}
              onTrash={onTrash}
            />
          </ul>
        </div>
      ))}
    </div>
  );
}
