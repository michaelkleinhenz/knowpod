import { DragEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Recording } from '../api/client';
import { isTask } from '../lib/labels';
import { noteType, statusLabel, title } from '../lib/recordings';
import { NoteIcon } from './Icons';

interface Props {
  rec: Recording;
  active: boolean;
  aiReady: boolean;
  // meta is shown at the end of the row (the time, or the date in the folder view).
  meta: string;
  onSetDone: (r: Recording, done: boolean) => void;
  onDragStart?: (e: DragEvent) => void;
}

// NoteRow is one note in the sidebar: its type icon (a check box for tasks), title,
// processing state and time.
export function NoteRow({ rec: r, active, aiReady, meta, onSetDone, onDragStart }: Props) {
  const { t } = useTranslation();
  const state = statusLabel(r, aiReady);
  const task = isTask(r);
  return (
    <li className={task ? `task-item${r.done ? ' done' : ''}` : undefined}>
      <Link
        to={`/conversations/${r.id}`}
        className={`conversation-item${active ? ' active' : ''}`}
        aria-current={active ? 'page' : undefined}
        draggable={!!onDragStart}
        onDragStart={onDragStart}
      >
        <NoteIcon type={noteType(r)} label={t(`conversations.types.${noteType(r)}`)} />
        <span className="conversation-title">
          {title(r)}
          {state && <span className={`state-pill${r.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
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
    </li>
  );
}
