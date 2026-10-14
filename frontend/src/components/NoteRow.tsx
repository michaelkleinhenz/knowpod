import { DragEvent, HTMLAttributes, MouseEvent, ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Recording } from '../api/client';
import { isTask } from '../lib/labels';
import { iconKind, statusLabel, title } from '../lib/recordings';
import { ChevronIcon, NewNoteIcon, NoteIcon, ShareIcon } from './Icons';
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
  // onSetDone stores the check mark; when it returns a promise, a checked-off row waits for it
  // before it comes back (a repeating task with its next date).
  onSetDone: (r: Recording, done: boolean) => void | Promise<void>;
  // taskDate shows a task's due date on the row (off where the list is grouped by it).
  taskDate?: boolean;
  // onNewSub, when set, shows a button (and a menu item) that adds a sub-note to the note.
  onNewSub?: (r: Recording) => void;
  // onTrash, when set, adds a menu item that moves the note to the trash.
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

// Checking a task off plays out in steps, so it's clear the click took: the check mark and the
// struck-through title show (CHECK_MS), then the row folds away (LEAVE_MS). Once it's stored,
// a row still listed (a repeating task at its next date) unfolds again as a new one (ENTER_MS).
const CHECK_MS = 450;
const LEAVE_MS = 300;
const ENTER_MS = 350;

type CheckPhase = 'idle' | 'checked' | 'leaving' | 'entering';

// renewing holds the tasks checked off and being stored: a row that comes back for one in a
// new place (a repeating task moved to another date group) unfolds there as the new one.
const renewing = new Set<string>();

const reducedMotion = () => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

// NoteRow is one note in the sidebar: in front, a toggle for its sub-notes if it has any (in a
// gutter every row has, so the rows' icons line up); then its type icon (a check box for
// tasks), title (shortened to fit, its number always shown, to link it with "#12"),
// processing state and time; and a button to add a sub-note. Its other actions are in its
// menu (right click or long press).
export function NoteRow({ rec: r, active, aiReady, meta, onSetDone, taskDate = true, onNewSub, onTrash, onDragStart, sub, lineProps, drop, inSharedFolder, children }: Props) {
  const { t } = useTranslation();
  const state = statusLabel(r, aiReady);
  const task = isTask(r);
  const notes = useNotesIfAny();
  // A note from someone else shows their avatar instead of the shared mark.
  const fromOther = !!noteOrigin(r, notes?.filterContext.userId);
  const tabs = useNoteTabs();
  const tabLink = useTabLink();
  const rowRef = useRef<HTMLLIElement>(null);
  const [phase, setPhase] = useState<CheckPhase>(() => (renewing.has(r.id) ? 'entering' : 'idle'));

  // checkOff plays the check-off steps around storing it; unchecking is stored right away.
  async function checkOff(done: boolean) {
    if (!done) {
      void onSetDone(r, false);
      return;
    }
    const motion = !reducedMotion();
    setPhase('checked');
    if (motion) await wait(CHECK_MS);
    setPhase('leaving');
    if (motion) await wait(LEAVE_MS);
    renewing.add(r.id);
    await onSetDone(r, true);
    // A row that's gone for good (a done task the list hides) leaves nothing behind.
    setTimeout(() => renewing.delete(r.id), ENTER_MS);
    setPhase('entering');
  }

  // The row folds from and unfolds to its own height.
  useLayoutEffect(() => {
    const el = rowRef.current;
    if (el && (phase === 'leaving' || phase === 'entering')) el.style.setProperty('--row-h', `${el.scrollHeight}px`);
  }, [phase]);

  useEffect(() => {
    if (phase !== 'entering') return;
    renewing.delete(r.id);
    const timer = setTimeout(() => setPhase('idle'), reducedMotion() ? 0 : ENTER_MS);
    return () => clearTimeout(timer);
  }, [phase]);

  const checked = phase === 'checked' || phase === 'leaving' || (phase === 'idle' && !!r.done) || (phase === 'entering' && !!r.done);
  // Right click or long press opens a menu: the note opens in a new tab (where tabs are
  // shown), gets a sub-note, and only the owner can duplicate it; last it goes to the trash.
  const { handlers: menuHandlers, menu } = useContextMenu([
    ...(tabs && tabsShown() ? [{ label: t('tabs.openInNewTab'), onSelect: () => tabs.open(r.id) }] : []),
    ...(onNewSub ? [{ label: t('subNotes.new'), onSelect: () => onNewSub(r) }] : []),
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
    ...(onTrash ? [{ label: t('conversation.moveToTrash'), danger: true, onSelect: () => onTrash(r) }] : []),
  ]);
  return (
    <li ref={rowRef} className={task ? `task-item${checked ? ' done' : ''}${phase !== 'idle' ? ` ${phase}` : ''}` : undefined}>
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
        {onNewSub && (
          <span className="tree-actions note-actions">
            <button type="button" className="icon-button" title={t('subNotes.new')} aria-label={t('subNotes.newLabel', { title: title(r) })} onClick={() => onNewSub(r)}>
              <NewNoteIcon />
            </button>
          </span>
        )}
        {/* Over the note's icon; outside the link so checking doesn't open the note. */}
        {task && (
          <input
            type="checkbox"
            className="task-check"
            checked={checked}
            disabled={r.access === 'viewer' || phase !== 'idle'}
            title={r.access === 'viewer' ? t('labels.viewOnly') : r.due?.repeat && !r.done ? t('labels.repeatHint') : undefined}
            onChange={(e) => void checkOff(e.target.checked)}
            aria-label={t('labels.doneLabel', { title: title(r) })}
          />
        )}
        {menu}
      </div>
      {children}
    </li>
  );
}
