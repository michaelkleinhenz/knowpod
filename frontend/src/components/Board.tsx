import { DragEvent, PointerEvent as ReactPointerEvent, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { api, Board as BoardSetup, BoardColumn, BoardScope, Folder, Recording, SavedFilter } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { FilterContext, Matcher, parseFilter } from '../lib/filterQuery';
import { flatTree, folderOf } from '../lib/folders';
import { isTask, labelName, labelStyle, noteLabels } from '../lib/labels';
import { TaskMeta } from './TaskControls';
import { iconKind, title, when } from '../lib/recordings';
import { GripIcon, NewNoteIcon, NoteIcon, PencilIcon, TrashIcon } from './Icons';

// DRAG_TYPE marks a card being dragged, so the columns ignore other drags (files, notes
// from the sidebar).
const DRAG_TYPE = 'application/x-knowpod-card';

// EDGE is how close (in px) to the edge of the board or the window a card dragged by touch
// has to come to scroll it; SPEED is the most it scrolls per frame.
const EDGE = 48;
const SPEED = 14;

// Grab is a card dragged by its grip with a finger or a pen (touch screens don't do HTML
// drag and drop): where the pointer is, and where on the card it was grabbed.
type Grab = { id: string; pointer: number; x: number; y: number; dx: number; dy: number; width: number };

// newColumnID makes an ID for a column added in the browser.
function newColumnID(): string {
  return Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
}

// ScopeSources are what a board's scope is resolved against: the user's folders and saved
// filters, and the labels and folders filter queries name.
export interface ScopeSources {
  folders: Folder[];
  filters: SavedFilter[];
  filterContext: FilterContext;
}

// scopeMatcher reports whether the board shows a note. Boards never show boards, nor notes
// in the trash; a filter that is gone or doesn't parse shows none.
function scopeMatcher(scope: BoardScope, src: ScopeSources): Matcher {
  let inScope: Matcher = () => false;
  switch (scope.kind) {
    case 'folder': {
      const folderIds = new Set(src.folders.map((f) => f.id));
      inScope = (r) => folderOf(r, folderIds) === scope.id;
      break;
    }
    case 'label':
      inScope = (r) => r.labels?.includes(scope.id) ?? false;
      break;
    case 'filter': {
      const f = src.filters.find((x) => x.id === scope.id);
      const parsed = f ? parseFilter(f.query, src.filterContext) : null;
      if (parsed?.ok) inScope = parsed.match;
      break;
    }
  }
  return (r) => r.type !== 'board' && !r.deletedAt && inScope(r);
}

// layout sorts the notes in the board's scope into its columns: in the order they were put
// there, and the notes in no column (newest first) at the end of the first column.
function layout(board: BoardSetup, notes: Recording[], src: ScopeSources): Recording[][] {
  const inScope = scopeMatcher(board.scope, src);
  const shown = new Map(notes.filter(inScope).map((r) => [r.id, r]));
  const placed = new Set<string>();
  const cols = board.columns.map((c) =>
    (c.notes ?? []).flatMap((id) => {
      const r = shown.get(id);
      if (!r || placed.has(id)) return [];
      placed.add(id);
      return [r];
    }),
  );
  const rest = [...shown.values()].filter((r) => !placed.has(r.id)).sort((a, b) => when(b).getTime() - when(a).getTime());
  cols[0]?.push(...rest);
  return cols;
}

// boardLanes finds the boards that show the note and the column it is in on each: the one it
// was put into, or the first column when it is in none (as the board shows it).
export function boardLanes(rec: Recording, notes: Recording[], src: ScopeSources): { board: Recording; lane: string }[] {
  return notes.flatMap((b) => {
    const setup = b.board;
    if (b.type !== 'board' || !setup || setup.columns.length === 0 || !scopeMatcher(setup.scope, src)(rec)) return [];
    const column = setup.columns.find((c) => c.notes?.includes(rec.id)) ?? setup.columns[0];
    return [{ board: b, lane: column.name }];
  });
}

function scopeValue(s: BoardScope): string {
  return s.kind ? `${s.kind}:${s.id}` : '';
}

function parseScope(v: string): BoardScope {
  const [kind, ...id] = v.split(':');
  return kind === 'folder' || kind === 'label' || kind === 'filter' ? { kind, id: id.join(':') } : { kind: '', id: '' };
}

// ColumnHeader shows a column's name and count; the name is renamed in place.
function ColumnHeader({
  column,
  count,
  canDelete,
  editing,
  setEditing,
  onRename,
  onDelete,
}: {
  column: BoardColumn;
  count: number;
  canDelete: boolean;
  editing: boolean;
  setEditing: (on: boolean) => void;
  onRename: (name: string) => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(column.name);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (editing) {
      setName(column.name);
      input.current?.select();
    }
  }, [editing, column.name]);

  const commit = () => {
    setEditing(false);
    const v = name.trim();
    if (v && v !== column.name) onRename(v);
  };

  return (
    <div className="board-column-head">
      {editing ? (
        <input
          ref={input}
          className="board-column-name-input"
          value={name}
          maxLength={60}
          aria-label={t('board.columnName')}
          onChange={(e) => setName(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commit();
            if (e.key === 'Escape') setEditing(false);
          }}
        />
      ) : (
        <h2 className="board-column-name" onDoubleClick={() => setEditing(true)}>
          {column.name}
          <span className="board-count">{count}</span>
        </h2>
      )}
      {!editing && (
        <span className="board-column-tools">
          <button type="button" className="icon-button small" title={t('board.renameColumn')} aria-label={t('board.renameColumnLabel', { name: column.name })} onClick={() => setEditing(true)}>
            <PencilIcon />
          </button>
          {canDelete && (
            <button type="button" className="icon-button small danger" title={t('board.deleteColumn')} aria-label={t('board.deleteColumnLabel', { name: column.name })} onClick={onDelete}>
              <TrashIcon />
            </button>
          )}
        </span>
      )}
    </div>
  );
}

// Board shows a board note: the notes of a folder, with a label or matching a saved filter as
// cards in columns. Cards are dragged between columns (or moved with their arrow buttons);
// columns are renamed, added and deleted. Every change is saved right away.
export function Board({ rec, setRec }: { rec: Recording; setRec: (r: Recording) => void }) {
  const { t } = useTranslation();
  const { recordings, folders, labels, filters, filterContext, upsert } = useNotes();
  const [board, setBoard] = useState<BoardSetup>(() => rec.board ?? { scope: { kind: '', id: '' }, columns: [] });
  const [error, setError] = useState<string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [dropAt, setDropAt] = useState<{ col: number; before: string | null } | null>(null);
  const [editingColumn, setEditingColumn] = useState<string | null>(null);
  const [grab, setGrab] = useState<Grab | null>(null);
  const columnsRef = useRef<HTMLDivElement>(null);
  // The latest drop target and pointer position, for the pointer handlers and the scrolling.
  const dropRef = useRef(dropAt);
  dropRef.current = dropAt;
  const grabRef = useRef(grab);
  grabRef.current = grab;
  // Only the answer to the latest save is taken over.
  const saves = useRef(0);

  // Take over changes saved elsewhere (e.g. the scope cleared because its label was deleted).
  const stored = JSON.stringify(rec.board ?? null);
  useEffect(() => {
    if (saves.current === 0 && rec.board) setBoard(rec.board);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stored]);

  const notes = recordings ?? [];
  const cols = useMemo(
    () => layout(board, notes, { folders: folders ?? [], filters: filters ?? [], filterContext }),
    [board, notes, folders, filters, filterContext],
  );

  async function save(next: BoardSetup) {
    const previous = board;
    setBoard(next);
    setError(null);
    const n = ++saves.current;
    try {
      const saved = await api.setBoard(rec.id, next);
      if (n !== saves.current) return;
      saves.current = 0;
      if (saved.board) setBoard(saved.board);
      setRec(saved);
    } catch (err) {
      if (n !== saves.current) return;
      saves.current = 0;
      setBoard(previous);
      setError(errorText(err, t));
    }
  }

  // withCards stores the cards as shown, in columns, keeping the placements of notes that
  // aren't shown (out of the scope for now). Notes that no longer exist are dropped.
  function withCards(shown: Recording[][], columns = board.columns): BoardSetup {
    const visible = new Set(shown.flat().map((r) => r.id));
    const exists = new Set(notes.map((r) => r.id));
    return {
      ...board,
      columns: columns.map((c, i) => {
        const hidden = (c.notes ?? []).filter((id) => !visible.has(id) && exists.has(id));
        return { ...c, notes: [...(shown[i] ?? []).map((r) => r.id), ...hidden] };
      }),
    };
  }

  function moveCard(id: string, col: number, before: string | null) {
    const card = cols.flat().find((r) => r.id === id);
    if (!card || before === id) return;
    const next = cols.map((list) => list.filter((r) => r.id !== id));
    const at = before ? next[col].findIndex((r) => r.id === before) : -1;
    next[col].splice(at < 0 ? next[col].length : at, 0, card);
    void save(withCards(next));
  }

  // moveBy moves a card to the column on the left (-1) or right (+1), to its end.
  const moveBy = (id: string, from: number, by: number) => moveCard(id, from + by, null);

  function renameColumn(i: number, name: string) {
    void save({ ...board, columns: board.columns.map((c, j) => (j === i ? { ...c, name } : c)) });
  }

  function addColumn() {
    const column = { id: newColumnID(), name: t('board.newColumnName'), notes: [] };
    setEditingColumn(column.id);
    void save({ ...board, columns: [...board.columns, column] });
  }

  function deleteColumn(i: number) {
    const c = board.columns[i];
    if (cols[i].length > 0 && !window.confirm(t('board.deleteColumnConfirm', { name: c.name, first: board.columns[i === 0 ? 1 : 0].name }))) return;
    // Its cards are in no column now, so they are shown in the first one.
    const shown = cols.filter((_, j) => j !== i);
    void save(withCards(shown, board.columns.filter((_, j) => j !== i)));
  }

  async function setDone(r: Recording, done: boolean) {
    upsert({ ...r, done });
    try {
      upsert(await api.setNoteDone(r.id, done));
    } catch (err) {
      upsert(r);
      setError(errorText(err, t));
    }
  }

  function onDragOver(e: DragEvent, col: number, before: string | null) {
    if (!e.dataTransfer.types.includes(DRAG_TYPE)) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = 'move';
    if (dropAt?.col !== col || dropAt.before !== before) setDropAt({ col, before });
  }

  function onDrop(e: DragEvent, col: number, before: string | null) {
    const id = e.dataTransfer.getData(DRAG_TYPE);
    if (!id) return;
    e.preventDefault();
    e.stopPropagation();
    setDragging(null);
    setDropAt(null);
    moveCard(id, col, before);
  }

  // dropTargetAt finds the column and card under a point, like onDragOver does for mouse drags.
  function dropTargetAt(x: number, y: number): { col: number; before: string | null } | null {
    const el = document.elementFromPoint(x, y);
    const column = el?.closest<HTMLElement>('[data-board-col]');
    if (!column || !columnsRef.current?.contains(column)) return null;
    const card = el?.closest<HTMLElement>('[data-board-card]');
    return { col: Number(column.dataset.boardCol), before: card?.dataset.boardCard ?? null };
  }

  function track(x: number, y: number) {
    const at = dropTargetAt(x, y);
    const cur = dropRef.current;
    if (at?.col !== cur?.col || at?.before !== cur?.before) setDropAt(at);
  }

  function onGripDown(e: ReactPointerEvent, id: string) {
    // Mice use HTML drag and drop on the whole card.
    if (e.pointerType === 'mouse' || e.button !== 0) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    const card = (e.currentTarget as HTMLElement).closest('li')!.getBoundingClientRect();
    setGrab({ id, pointer: e.pointerId, x: e.clientX, y: e.clientY, dx: e.clientX - card.left, dy: e.clientY - card.top, width: card.width });
    setDragging(id);
    setDropAt(null);
  }

  function onGripMove(e: ReactPointerEvent) {
    const g = grabRef.current;
    if (!g || e.pointerId !== g.pointer) return;
    setGrab({ ...g, x: e.clientX, y: e.clientY });
    track(e.clientX, e.clientY);
  }

  function onGripEnd(e: ReactPointerEvent, drop: boolean) {
    const g = grabRef.current;
    if (!g || e.pointerId !== g.pointer) return;
    const at = drop ? dropTargetAt(e.clientX, e.clientY) : null;
    setGrab(null);
    setDragging(null);
    setDropAt(null);
    if (at) moveCard(g.id, at.col, at.before);
  }

  // While a card is dragged by touch, scroll the board sideways and the page up and down when
  // it comes near their edges, and keep the drop target under the finger up to date.
  const grabbing = grab !== null;
  useEffect(() => {
    if (!grabbing) return;
    let frame = 0;
    const step = () => {
      const g = grabRef.current;
      const box = columnsRef.current;
      if (g && box) {
        const r = box.getBoundingClientRect();
        const speed = (d: number) => Math.ceil(SPEED * Math.min(1, Math.max(0, (EDGE - d) / EDGE)));
        const dx = speed(g.x - r.left) > 0 ? -speed(g.x - r.left) : speed(r.right - g.x);
        const dy = speed(g.y) > 0 ? -speed(g.y) : speed(window.innerHeight - g.y);
        if (dx) box.scrollLeft += dx;
        if (dy) window.scrollBy(0, dy);
        if (dx || dy) track(g.x, g.y);
      }
      frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [grabbing]);

  const grabbed = grab ? cols.flat().find((r) => r.id === grab.id) : undefined;

  const tree = flatTree(folders ?? []);
  const scopeMissing =
    (board.scope.kind === 'folder' && board.scope.id !== '' && folders !== null && !folders.some((f) => f.id === board.scope.id)) ||
    (board.scope.kind === 'label' && labels !== null && !labels.some((l) => l.id === board.scope.id)) ||
    (board.scope.kind === 'filter' && filters !== null && !filters.some((f) => f.id === board.scope.id));

  return (
    <div className="board">
      <div className="board-bar">
        <label className="board-scope">
          <span>{t('board.scope')}</span>
          <select value={scopeMissing ? '' : scopeValue(board.scope)} onChange={(e) => void save({ ...board, scope: parseScope(e.target.value) })}>
            <option value="">{t('board.scopeNone')}</option>
            <optgroup label={t('board.folders')}>
              <option value="folder:">{t('folders.topLevel')}</option>
              {tree.map(({ folder, depth }) => (
                <option key={folder.id} value={`folder:${folder.id}`}>
                  {' '.repeat(depth)}
                  {folder.name}
                </option>
              ))}
            </optgroup>
            <optgroup label={t('board.labels')}>
              {(labels ?? []).map((l) => (
                <option key={l.id} value={`label:${l.id}`}>
                  {labelName(l)}
                </option>
              ))}
            </optgroup>
            {!!filters?.length && (
              <optgroup label={t('board.filters')}>
                {filters.map((f) => (
                  <option key={f.id} value={`filter:${f.id}`}>
                    {f.name}
                  </option>
                ))}
              </optgroup>
            )}
          </select>
        </label>
        <button type="button" className="pill-button" onClick={addColumn} disabled={board.columns.length >= 20}>
          <NewNoteIcon /> <span>{t('board.addColumn')}</span>
        </button>
      </div>
      {error && <p className="error">{error}</p>}
      {(!board.scope.kind || scopeMissing) && <p className="notice">{t(scopeMissing ? 'board.scopeMissing' : 'board.scopeHint')}</p>}
      {board.scope.kind && !scopeMissing && recordings && cols.every((c) => c.length === 0) && <p className="muted">{t('board.empty')}</p>}

      <div className="board-columns" ref={columnsRef}>
        {board.columns.map((c, i) => (
          <section
            key={c.id}
            className={`board-column${dropAt?.col === i && dropAt.before === null ? ' drop-target' : ''}`}
            aria-label={c.name}
            data-board-col={i}
            onDragOver={(e) => onDragOver(e, i, null)}
            onDragLeave={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropAt(null);
            }}
            onDrop={(e) => onDrop(e, i, null)}
          >
            <ColumnHeader
              column={c}
              count={cols[i].length}
              canDelete={board.columns.length > 1}
              editing={editingColumn === c.id}
              setEditing={(on) => setEditingColumn(on ? c.id : null)}
              onRename={(name) => renameColumn(i, name)}
              onDelete={() => deleteColumn(i)}
            />
            <ul className="board-cards">
              {cols[i].map((r) => (
                <li
                  key={r.id}
                  className={`board-card${dragging === r.id ? ' dragging' : ''}${dropAt?.col === i && dropAt.before === r.id ? ' drop-before' : ''}${isTask(r) && r.done ? ' done' : ''}`}
                  data-board-card={r.id}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData(DRAG_TYPE, r.id);
                    e.dataTransfer.effectAllowed = 'move';
                    setDragging(r.id);
                  }}
                  onDragEnd={() => {
                    setDragging(null);
                    setDropAt(null);
                  }}
                  onDragOver={(e) => onDragOver(e, i, r.id)}
                  onDrop={(e) => onDrop(e, i, r.id)}
                >
                  <div className="board-card-main">
                    <span
                      className="board-card-grip"
                      role="img"
                      aria-label={t('board.dragCard', { title: title(r) })}
                      onPointerDown={(e) => onGripDown(e, r.id)}
                      onPointerMove={onGripMove}
                      onPointerUp={(e) => onGripEnd(e, true)}
                      onPointerCancel={(e) => onGripEnd(e, false)}
                    >
                      <GripIcon />
                    </span>
                    {isTask(r) ? (
                      <input
                        type="checkbox"
                        className="board-card-check"
                        checked={!!r.done}
                        onChange={(e) => void setDone(r, e.target.checked)}
                        aria-label={t('labels.doneLabel', { title: title(r) })}
                      />
                    ) : (
                      <NoteIcon type={iconKind(r)} label={t(`conversations.types.${iconKind(r)}`)} />
                    )}
                    <Link to={`/conversations/${r.id}`} className="board-card-title" draggable={false}>
                      {title(r)}
                    </Link>
                    <span className="board-card-move">
                      {[-1, 1].map((by) => {
                        const target = board.columns[i + by];
                        return (
                          <button
                            key={by}
                            type="button"
                            className="icon-button small"
                            disabled={!target}
                            title={target ? t('board.moveTo', { name: target.name }) : undefined}
                            aria-label={target ? t('board.moveCardTo', { title: title(r), name: target.name }) : t(by < 0 ? 'board.moveLeft' : 'board.moveRight')}
                            onClick={() => moveBy(r.id, i, by)}
                          >
                            {by < 0 ? '‹' : '›'}
                          </button>
                        );
                      })}
                    </span>
                  </div>
                  {(r.labels?.length ?? 0) > 0 && (
                    <div className="board-card-labels">
                      {isTask(r) && <TaskMeta rec={r} />}
                      {noteLabels(r, labels).map((l) => (
                        <span key={l.id} className="label-chip small" style={labelStyle(l)}>
                          <span className="label-dot" aria-hidden="true" />
                          {labelName(l)}
                        </span>
                      ))}
                    </div>
                  )}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
      {grab && grabbed && (
        <div className="board-card board-card-ghost" style={{ left: grab.x - grab.dx, top: grab.y - grab.dy, width: grab.width }} aria-hidden="true">
          <div className="board-card-main">
            <span className="board-card-grip">
              <GripIcon />
            </span>
            <span className="board-card-title">{title(grabbed)}</span>
          </div>
        </div>
      )}
    </div>
  );
}
