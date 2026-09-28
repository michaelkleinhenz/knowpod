import { DragEvent, HTMLAttributes, MouseEvent, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Recording } from '../api/client';
import { isTask } from '../lib/labels';
import { iconKind, statusLabel, title } from '../lib/recordings';
import { ChevronIcon, NewNoteIcon, NoteIcon, ShareIcon } from './Icons';
import { TaskMeta } from './TaskControls';

interface Props {
  rec: Recording;
  active: boolean;
  aiReady: boolean;
  // meta is shown at the end of the row (the time, or the date in the folder view).
  meta: string;
  onSetDone: (r: Recording, done: boolean) => void;
  // taskDate shows a task's due date on the row (off where the list is grouped by it).
  taskDate?: boolean;
  // onNewSub, when set, shows a button that adds a sub-note to the note.
  onNewSub?: (r: Recording) => void;
  onDragStart?: (e: DragEvent) => void;
  // sub is set for a note with sub-notes: how many, and whether they are shown below it.
  sub?: { count: number; open: boolean; onToggle: (e: MouseEvent) => void };
  // lineProps go on the row itself (e.g. to drop notes onto it); drop highlights it, or a
  // line before or after it (where a dropped note goes).
  lineProps?: HTMLAttributes<HTMLDivElement>;
  drop?: boolean | 'before' | 'after';
  // children are shown below the row (the sub-notes).
  children?: ReactNode;
}

// NoteRow is one note in the sidebar: in front, a toggle for its sub-notes if it has any and
// a button to add a sub-note (both in a gutter every row has, so the rows' icons and times
// line up); then its type icon (a check box for tasks), title (shortened to fit, its number
// always shown, to link it with "#12"), processing state and time.
export function NoteRow({ rec: r, active, aiReady, meta, onSetDone, taskDate = true, onNewSub, onDragStart, sub, lineProps, drop, children }: Props) {
  const { t } = useTranslation();
  const state = statusLabel(r, aiReady);
  const task = isTask(r);
  return (
    <li className={task ? `task-item${r.done ? ' done' : ''}` : undefined}>
      <div {...lineProps} className={`note-line${drop === true ? ' drop' : drop ? ` drop-${drop}` : ''}`}>
        <span className="note-gutter">
          {sub && (
            <button
              type="button"
              className="note-sub-toggle"
              aria-expanded={sub.open}
              title={`${t(sub.open ? 'subNotes.hide' : 'subNotes.show')} (${t('subNotes.allHint')})`}
              aria-label={t(sub.open ? 'subNotes.hideLabel' : 'subNotes.showLabel', { title: title(r), count: sub.count })}
              onClick={sub.onToggle}
            >
              <ChevronIcon open={sub.open} />
            </button>
          )}
          {onNewSub && (
            <button type="button" className="note-add-sub" title={t('subNotes.new')} aria-label={t('subNotes.newLabel', { title: title(r) })} onClick={() => onNewSub(r)}>
              <NewNoteIcon />
            </button>
          )}
        </span>
        <Link
          to={`/conversations/${r.id}`}
          className={`conversation-item${active ? ' active' : ''}`}
          aria-current={active ? 'page' : undefined}
          title={title(r)}
          draggable={!!onDragStart}
          onDragStart={onDragStart}
        >
          <NoteIcon type={iconKind(r)} label={t(`conversations.types.${iconKind(r)}`)} />
          <span className="conversation-title">
            <span className="note-title-line">
              <span className="note-title-text">{title(r)}</span>
              {r.shared && (
                <span className="note-row-shared" title={t('sharing.badge')} aria-label={t('sharing.badge')}>
                  <ShareIcon size={12} />
                </span>
              )}
              {r.number ? <span className="note-row-number">#{r.number}</span> : null}
              {sub && <span className="tree-count note-sub-count">{sub.count}</span>}
            </span>
            {/* Hidden while empty (CSS). */}
            {(task || state) && (
              <span className="note-title-extra">
                {task && <TaskMeta rec={r} showDue={taskDate} />}
                {state && <span className={`state-pill${r.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
              </span>
            )}
          </span>
          <span className="conversation-time">{meta}</span>
        </Link>
        {/* Over the note's icon; outside the link so checking doesn't open the note. */}
        {task && (
          <input
            type="checkbox"
            className="task-check"
            checked={!!r.done}
            onChange={(e) => onSetDone(r, e.target.checked)}
            aria-label={t('labels.doneLabel', { title: title(r) })}
          />
        )}
      </div>
      {children}
    </li>
  );
}
