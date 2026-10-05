import { DragEvent, HTMLAttributes, MouseEvent, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Recording } from '../api/client';
import { isTask } from '../lib/labels';
import { iconKind, statusLabel, title } from '../lib/recordings';
import { ChevronIcon, NewNoteIcon, NoteIcon, ShareIcon, TrashIcon } from './Icons';
import { noteOrigin, OriginBadge } from './PersonBadge';
import { TaskMeta } from './TaskControls';
import { useNotesIfAny } from '../context/NotesContext';
import { api } from '../api/client';
import { errorText } from '../lib/errors';
import { useContextMenu } from './ContextMenu';
import { tabsShown, useNoteTabs, useTabLink } from '../context/NoteTabs';

interface Props {
  rec: Recording;
  active: boolean;
  aiReady: boolean;
  // meta is shown at the end of the row (the time or date; none in the folder view).
  meta: string;
  onSetDone: (r: Recording, done: boolean) => void;
  // taskDate shows a task's due date on the row (off where the list is grouped by it).
  taskDate?: boolean;
  // onNewSub, when set, shows a button that adds a sub-note to the note.
  onNewSub?: (r: Recording) => void;
  // onTrash, when set, shows a button that moves the note to the trash.
  onTrash?: (r: Recording) => void;
  onDragStart?: (e: DragEvent) => void;
  // sub is set for a note with sub-notes: how many, and whether they are shown below it.
  sub?: { count: number; open: boolean; onToggle: (e: MouseEvent) => void };
  // lineProps go on the row itself (e.g. to drop notes onto it); drop highlights it, or a
  // line before or after it (where a dropped note goes).
  lineProps?: HTMLAttributes<HTMLDivElement>;
  drop?: boolean | 'before' | 'after';
  // inSharedFolder leaves out the note's shared mark: its folder shows it.
  inSharedFolder?: boolean;
  // children are shown below the row (the sub-notes).
  children?: ReactNode;
}

// NoteRow is one note in the sidebar: in front, a toggle for its sub-notes if it has any (in a
// gutter every row has, so the rows' icons line up); then its type icon (a check box for
// tasks), title (shortened to fit, its number always shown, to link it with "#12"),
// processing state and time; and buttons to add a sub-note and move it to the trash.
export function NoteRow({ rec: r, active, aiReady, meta, onSetDone, taskDate = true, onNewSub, onTrash, onDragStart, sub, lineProps, drop, inSharedFolder, children }: Props) {
  const { t } = useTranslation();
  const state = statusLabel(r, aiReady);
  const task = isTask(r);
  const notes = useNotesIfAny();
  // A note from someone else shows their avatar instead of the shared mark.
  const fromOther = !!noteOrigin(r, notes?.filterContext.userId);
  const tabs = useNoteTabs();
  const tabLink = useTabLink();
  // Right click or long press opens a menu: the note opens in a new tab (where tabs are
  // shown), and only the owner can duplicate it.
  const { handlers: menuHandlers, menu } = useContextMenu([
    ...(tabs && tabsShown() ? [{ label: t('tabs.openInNewTab'), onSelect: () => tabs.open(r.id) }] : []),
    ...(notes && (r.access ?? 'owner') === 'owner'
      ? [
          {
            label: t('folders.duplicate'),
            onSelect: () => {
              api
                .duplicateNote(r.id)
                .then(() => notes.reload())
                .catch((err) => window.alert(errorText(err, t)));
            },
          },
        ]
      : []),
  ]);
  return (
    <li className={task ? `task-item${r.done ? ' done' : ''}` : undefined}>
      <div {...lineProps} {...menuHandlers} className={`note-line${drop === true ? ' drop' : drop ? ` drop-${drop}` : ''}`}>
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
        </span>
        <Link
          to={`/conversations/${r.id}`}
          className={`conversation-item${active ? ' active' : ''}`}
          aria-current={active ? 'page' : undefined}
          title={title(r)}
          draggable={!!onDragStart}
          onDragStart={onDragStart}
          {...tabLink(r.id)}
        >
          <NoteIcon type={iconKind(r)} label={t(`conversations.types.${iconKind(r)}`)} />
          <span className="conversation-title">
            <span className="note-title-line">
              <span className="note-title-text">{title(r)}</span>
              {fromOther && <OriginBadge rec={r} size={15} />}
              {r.shared && !inSharedFolder && !fromOther && (
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
          {meta && <span className="conversation-time">{meta}</span>}
        </Link>
        {/* Shown over the end of the row on hover (always on touch screens), like a folder's. */}
        {(onTrash || onNewSub) && (
          <span className="tree-actions note-actions">
            {onNewSub && (
              <button type="button" className="icon-button" title={t('subNotes.new')} aria-label={t('subNotes.newLabel', { title: title(r) })} onClick={() => onNewSub(r)}>
                <NewNoteIcon />
              </button>
            )}
            {onTrash && (
              <button type="button" className="icon-button danger" title={t('conversation.moveToTrash')} aria-label={t('conversation.moveToTrashLabel', { title: title(r) })} onClick={() => onTrash(r)}>
                <TrashIcon />
              </button>
            )}
          </span>
        )}
        {/* Over the note's icon; outside the link so checking doesn't open the note. */}
        {task && (
          <input
            type="checkbox"
            className={`task-check${r.priority ? ` p${r.priority}` : ''}`}
            checked={!!r.done}
            disabled={r.access === 'viewer'}
            title={r.access === 'viewer' ? t('labels.viewOnly') : r.due?.repeat && !r.done ? t('labels.repeatHint') : undefined}
            onChange={(e) => onSetDone(r, e.target.checked)}
            aria-label={t('labels.doneLabel', { title: title(r) })}
          />
        )}
        {menu}
      </div>
      {children}
    </li>
  );
}
