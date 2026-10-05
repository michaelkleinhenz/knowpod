import { DragEvent, FormEvent, KeyboardEvent, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Folder, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import type { Matcher } from '../lib/filterQuery';
import { childFolders, folderOf, isInside, isUnderNote, notePath, sortInPlace } from '../lib/folders';
import { lastFolder, setLastFolder } from '../lib/lastFolder';
import { setOpen, useOpen } from '../lib/treeOpen';
import { ChevronIcon, FolderIcon, NewFolderIcon, NewNoteIcon, PencilIcon, ShareIcon, TrashIcon } from './Icons';
import { ShareFolder } from './ShareNote';
import { MenuDiv } from './ContextMenu';
import { NoteTreeRows, useNoteTree } from './NoteTree';

// Drag data types; the browser only reveals the types (not the data) while dragging over.
export const NOTE_TYPE = 'application/x-knowpod-note';
const FOLDER_TYPE = 'application/x-knowpod-folder';
const NO_FOLDERS: Folder[] = [];

// Editing is the inline name field: renaming folder id, or a new folder in parentId.
type Editing = { id?: string; parentId: string; name: string };

// Where is where an item dropped onto a row goes: into it, or before or after it.
type Where = 'into' | 'before' | 'after';

function dragged(e: DragEvent): 'note' | 'folder' | null {
  const types = e.dataTransfer.types;
  return types.includes(NOTE_TYPE) ? 'note' : types.includes(FOLDER_TYPE) ? 'folder' : null;
}

// dropEdge tells whether the pointer is over the top or bottom edge of the row (to put the
// item before or after it) or over its middle (to put it into it).
function dropEdge(e: DragEvent): Where {
  const rect = e.currentTarget.getBoundingClientRect();
  const y = (e.clientY - rect.top) / (rect.height || 1);
  return y < 0.3 ? 'before' : y > 0.7 ? 'after' : 'into';
}

// dropKey is the drop target shown for row key: the row itself, or a line before or after it.
const dropKey = (key: string, where: Where) => (where === 'into' ? key : `${where}:${key}`);

// dropClass highlights row key as the drop target, or a line before or after it.
function dropClass(target: string | null, key: string): string {
  if (target === key) return ' drop';
  if (target === `before:${key}`) return ' drop-before';
  if (target === `after:${key}`) return ' drop-after';
  return '';
}

// placed puts item before or after target in list, which it is taken out of first.
function placed<T extends { id: string }>(list: T[], item: T, target: T, where: 'before' | 'after'): T[] {
  const out = list.filter((x) => x.id !== item.id);
  const i = out.findIndex((x) => x.id === target.id);
  out.splice(i < 0 ? out.length : i + (where === 'after' ? 1 : 0), 0, item);
  return out;
}

interface Props {
  notes: Recording[];
  // search is the search or filter the list is narrowed by; null when there is none.
  search: Matcher | null;
  activeId?: string;
  aiReady: boolean;
  onSetDone: (r: Recording, done: boolean) => void;
  onNewSub?: (parent: Recording) => void;
  onTrash?: (r: Recording) => void;
  // onNewInFolder, when set, shows a button on each folder that adds a note to it.
  onNewInFolder?: (folderId: string) => void;
  // newFolder is bumped by the list's "New folder" button to start a folder at the top level.
  newFolder: number;
}

// FolderTree shows the notes in their folders, like files. Folders can be created, renamed,
// deleted and nested; notes and folders are moved by dragging them onto a folder (or onto
// the free space below, for the top level). Notes with sub-notes open like folders; a note
// dropped onto another note becomes its sub-note. The folders and notes in a place keep the
// order the user puts them in: dropped onto the top or bottom edge of a row, an item goes
// before or after it, and Alt+Up/Down moves the focused item up or down. The folder the
// user last opened is remembered, for new notes to go into.
export function FolderTree({ notes, search, activeId, aiReady, onSetDone, onNewSub, onTrash, onNewInFolder, newFolder }: Props) {
  const { t } = useTranslation();
  const { folders, recordings, reloadFolders, reload, upsert } = useNotes();
  const open = useOpen();
  const [editing, setEditing] = useState<Editing | null>(null);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const saving = useRef(false);

  // Before paint, so the name field is there (and focused) for the first key typed.
  useLayoutEffect(() => {
    if (newFolder) setEditing({ parentId: '', name: '' });
  }, [newFolder]);

  const all = folders ?? NO_FOLDERS;
  // The tree is built from all notes, so that sub-notes found by a search show under their
  // parents; notes lists the ones to show.
  const allNotes = recordings ?? notes;
  const searching = search !== null;
  const tree = useNoteTree(allNotes, notes);
  const { ids, children, notesIn, counts } = useMemo(() => {
    const ids = new Set(all.map((f) => f.id));
    // notesIn are the notes directly in each folder; sub-notes are under their parents.
    const notesIn = new Map<string, Recording[]>();
    for (const r of tree.roots) {
      const key = folderOf(r, ids);
      notesIn.set(key, [...(notesIn.get(key) ?? []), r]);
    }
    for (const [k, list] of notesIn) notesIn.set(k, sortInPlace(list));
    const children = childFolders(all);
    // counts are the notes in each folder, including its folders and sub-notes.
    const counts = new Map<string, number>();
    const count = (id: string): number => {
      const n = (notesIn.get(id) ?? []).reduce((s, r) => s + (tree.shown.get(r.id) ?? 0), 0) + (children.get(id) ?? []).reduce((s, f) => s + count(f.id), 0);
      counts.set(id, n);
      return n;
    };
    count('');
    return { ids, children, notesIn, counts };
  }, [all, tree]);

  const toggle = (id: string, on = !open.has(id)) => setOpen([id], on);
  // The shared folders mark their notes as shared.
  const sharedFolders = useMemo(() => new Map(all.filter((f) => f.shared).map((f) => [f.id, true])), [all]);

  // openFolder opens or closes a folder the user clicked. New notes go into the folder last
  // opened; closing it (or a folder it is in) makes that the folder it was in.
  const openFolder = (f: Folder, on = !open.has(f.id)) => {
    toggle(f.id, on);
    if (on) setLastFolder(f.id);
    else if (lastFolder() && isInside(lastFolder(), f.id, all)) setLastFolder(f.parentId ?? '');
  };

  // siblingsOf lists the notes in the same place as note r (its folder, or under its parent
  // note), in their order, and that place.
  function siblingsOf(r: Recording): { list: Recording[]; parentId?: string; folderId: string } {
    if (r.parentId && tree.subs.get(r.parentId)?.some((n) => n.id === r.id)) return { list: tree.subs.get(r.parentId) ?? [], parentId: r.parentId, folderId: '' };
    const folderId = folderOf(r, ids);
    return { list: notesIn.get(folderId) ?? [], folderId };
  }

  // focusRow puts the keyboard focus back on a row after it moved.
  const treeRef = useRef<HTMLDivElement>(null);
  const focusRow = (selector: string) => requestAnimationFrame(() => treeRef.current?.querySelector<HTMLElement>(selector)?.focus());

  // The open note is shown: the folders and notes above it open when it is opened.
  const revealed = useRef<string | undefined>(undefined);
  useEffect(() => {
    const r = activeId ? allNotes.find((n) => n.id === activeId) : undefined;
    if (!r || !folders || revealed.current === activeId) return;
    revealed.current = activeId;
    const parents = notePath(r, allNotes);
    const ids = parents.map((p) => p.id);
    const byId = new Map(folders.map((f) => [f.id, f]));
    for (let f = byId.get((parents[0] ?? r).folderId ?? ''); f && ids.length < 64; f = f.parentId ? byId.get(f.parentId) : undefined) ids.push(f.id);
    setOpen(ids, true);
  }, [activeId, allNotes, folders]);

  async function run(fn: () => Promise<void>) {
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  // saveName creates or renames the folder being edited; an empty or unchanged name cancels.
  async function saveName(e?: FormEvent) {
    e?.preventDefault();
    if (!editing || saving.current) return;
    const name = editing.name.trim();
    const current = editing.id ? all.find((f) => f.id === editing.id) : undefined;
    if (!name || name === current?.name) {
      setEditing(null);
      return;
    }
    saving.current = true;
    await run(async () => {
      if (current) await api.updateFolder(current.id, { name, parentId: current.parentId });
      else {
        await api.createFolder({ name, parentId: editing.parentId || undefined });
        if (editing.parentId) toggle(editing.parentId, true);
      }
      await reloadFolders();
      setEditing(null);
    });
    saving.current = false;
  }

  async function remove(f: Folder) {
    if (!window.confirm(t('folders.deleteConfirm', { name: f.name }))) return;
    await run(async () => {
      await api.deleteFolder(f.id);
      await Promise.all([reloadFolders(), reload()]);
    });
  }

  async function duplicate(f: Folder) {
    await run(async () => {
      await api.duplicateFolder(f.id);
      await Promise.all([reloadFolders(), reload()]);
    });
  }

  async function moveNote(id: string, folderId: string) {
    const r = allNotes.find((n) => n.id === id);
    if (!r || ((r.folderId ?? '') === folderId && !r.parentId)) return;
    upsert({ ...r, folderId: folderId || undefined, parentId: undefined, position: undefined });
    await run(async () => {
      try {
        upsert(await api.setNoteFolder(id, folderId));
      } catch (err) {
        upsert(r);
        throw err;
      }
    });
  }

  // moveUnder makes note id a sub-note of parentId, unless that would put it under itself.
  async function moveUnder(id: string, parentId: string) {
    const r = allNotes.find((n) => n.id === id);
    if (!r || r.parentId === parentId || isUnderNote(parentId, id, allNotes)) return;
    upsert({ ...r, parentId, folderId: undefined, position: undefined });
    toggle(parentId, true);
    await run(async () => {
      try {
        upsert(await api.setNoteParent(id, parentId));
      } catch (err) {
        upsert(r);
        throw err;
      }
    });
  }

  // placeNote puts note id before or after note target, in target's place, moving it there
  // first when it is elsewhere, and stores the new order of that place.
  async function placeNote(id: string, target: Recording, where: 'before' | 'after') {
    const r = allNotes.find((n) => n.id === id);
    if (!r || r.id === target.id) return;
    const place = siblingsOf(target);
    if (place.parentId && isUnderNote(place.parentId, id, allNotes)) return;
    const moving = siblingsOf(r).list !== place.list;
    const moved: Recording = moving ? { ...r, parentId: place.parentId, folderId: place.folderId || undefined } : r;
    const list = placed(place.list, moved, target, where);
    const before = new Map(list.map((n) => [n.id, allNotes.find((x) => x.id === n.id) ?? n]));
    list.forEach((n, i) => upsert({ ...n, position: i + 1 }));
    await run(async () => {
      try {
        if (moving) {
          if (place.parentId) await api.setNoteParent(id, place.parentId);
          else await api.setNoteFolder(id, place.folderId);
        }
        await api.reorderNotes(list.map((n) => n.id));
      } catch (err) {
        before.forEach((n) => upsert(n));
        throw err;
      }
    });
  }

  // placeFolder puts folder id before or after folder target, in target's parent, moving it
  // there first when it is elsewhere, and stores the new order of that parent.
  async function placeFolder(id: string, target: Folder, where: 'before' | 'after') {
    const f = all.find((x) => x.id === id);
    const parentId = target.parentId ?? '';
    if (!f || f.id === target.id || (parentId && isInside(parentId, id, all))) return;
    const list = placed(children.get(parentId) ?? [], f, target, where);
    await run(async () => {
      try {
        if ((f.parentId ?? '') !== parentId) await api.updateFolder(id, { name: f.name, parentId: parentId || undefined });
        await api.reorderFolders(list.map((x) => x.id));
      } finally {
        await reloadFolders();
      }
    });
  }

  // shiftKeys moves the focused note or folder up or down among the items in its place with
  // Alt+Up/Down.
  function shiftKeys<T extends { id: string }>(item: T, list: () => T[], place: (target: T, where: 'before' | 'after') => Promise<void>, selector: string) {
    return (e: KeyboardEvent) => {
      if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
      e.preventDefault();
      e.stopPropagation();
      const items = list();
      const target = items[items.findIndex((x) => x.id === item.id) + (e.key === 'ArrowUp' ? -1 : 1)];
      if (!target) return;
      void place(target, e.key === 'ArrowUp' ? 'before' : 'after').then(() => focusRow(selector));
    };
  }

  async function moveFolder(id: string, parentId: string) {
    const f = all.find((x) => x.id === id);
    if (!f || (f.parentId ?? '') === parentId || (parentId && isInside(parentId, id, all))) return;
    await run(async () => {
      await api.updateFolder(id, { name: f.name, parentId: parentId || undefined });
      await reloadFolders();
    });
  }

  // Drop handlers for a folder (id) or the top level (''). A folder dropped onto the top or
  // bottom edge of another folder's row goes before or after it.
  const dropProps = (id: string) => {
    const where = (e: DragEvent): Where => (id && dragged(e) === 'folder' ? dropEdge(e) : 'into');
    return {
      onDragOver: (e: DragEvent) => {
        if (!dragged(e)) return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropTarget(dropKey(id, where(e)));
      },
      onDragLeave: (e: DragEvent) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropTarget((d) => (d === id || d === `before:${id}` || d === `after:${id}` ? null : d));
      },
      onDrop: (e: DragEvent) => {
        const kind = dragged(e);
        if (!kind) return;
        e.preventDefault();
        e.stopPropagation();
        setDropTarget(null);
        const item = e.dataTransfer.getData(kind === 'note' ? NOTE_TYPE : FOLDER_TYPE);
        const w = where(e);
        const target = all.find((f) => f.id === id);
        if (w !== 'into' && target) {
          void placeFolder(item, target, w);
          return;
        }
        if (kind === 'note') void moveNote(item, id);
        else if (item !== id) void moveFolder(item, id);
        if (id) toggle(id, true);
      },
    };
  };

  // Drop handlers for a note: notes dropped onto it become its sub-notes; dropped onto the
  // top or bottom edge of its row, they go before or after it. Alt+Up/Down moves it.
  const noteDropProps = (r: Recording) => {
    const key = `note:${r.id}`;
    return {
      onDragOver: (e: DragEvent) => {
        if (dragged(e) !== 'note') return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropTarget(dropKey(key, dropEdge(e)));
      },
      onDragLeave: (e: DragEvent) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropTarget((d) => (d === key || d === `before:${key}` || d === `after:${key}` ? null : d));
      },
      onDrop: (e: DragEvent) => {
        if (dragged(e) !== 'note') return;
        e.preventDefault();
        e.stopPropagation();
        setDropTarget(null);
        const id = e.dataTransfer.getData(NOTE_TYPE);
        const where = dropEdge(e);
        if (where === 'into') void moveUnder(id, r.id);
        else void placeNote(id, r, where);
      },
      onKeyDown: shiftKeys(
        r,
        () => siblingsOf(r).list,
        (target, where) => placeNote(r.id, target, where),
        `a[href="/conversations/${CSS.escape(r.id)}"]`,
      ),
    };
  };

  const nameField = (
    <form className="tree-rename" onSubmit={saveName}>
      <span className="tree-icon">
        <FolderIcon />
      </span>
      <input
        autoFocus
        maxLength={80}
        value={editing?.name ?? ''}
        placeholder={t('folders.namePlaceholder')}
        aria-label={t('folders.name')}
        onChange={(e) => setEditing((ed) => ed && { ...ed, name: e.target.value })}
        onBlur={() => void saveName()}
        onKeyDown={(e) => e.key === 'Escape' && setEditing(null)}
      />
    </form>
  );

  // noteRows shows notes with their sub-notes, which open and close like folders.
  const noteRows = (list: Recording[]) => (
    <NoteTreeRows
      list={list}
      tree={tree}
      searching={searching}
      activeId={activeId}
      aiReady={aiReady}
      meta={() => ''}
      onSetDone={onSetDone}
      onNewSub={onNewSub}
      onTrash={onTrash}
      rowProps={(r) => ({
        onDragStart: (e) => {
          e.dataTransfer.setData(NOTE_TYPE, r.id);
          e.dataTransfer.effectAllowed = 'move';
        },
        lineProps: noteDropProps(r),
        drop: dropTarget === `note:${r.id}` || (dropTarget === `before:note:${r.id}` ? 'before' : dropTarget === `after:note:${r.id}` ? 'after' : false),
        inSharedFolder: !!sharedFolders.get((notePath(r, allNotes)[0] ?? r).folderId ?? ''),
      })}
    />
  );

  const folderRows = (parent: string) =>
    (children.get(parent) ?? []).map((f) => {
      // While searching, only folders with matches are shown, all of them open.
      if (searching && !counts.get(f.id)) return null;
      const isOpen = searching || open.has(f.id);
      const renaming = editing?.id === f.id;
      // A folder shared with the user is its owner's to change; editors add notes and
      // folders to it.
      const own = (f.access ?? 'owner') === 'owner';
      const canAdd = own || f.access === 'editor';
      // Folders shared with the user can still be filed in the user's own tree.
      const canMove = own || !!f.movable;
      return (
        <li key={f.id} className="tree-folder">
          {renaming ? (
            nameField
          ) : (
            <MenuDiv
              items={own && !f.remarkable ? [{ label: t('folders.duplicate'), onSelect: () => void duplicate(f) }] : []}
              className={`tree-row${dropClass(dropTarget, f.id)}`}
              data-folder={f.id}
              draggable={canMove}
              onDragStart={(e) => {
                e.dataTransfer.setData(FOLDER_TYPE, f.id);
                e.dataTransfer.effectAllowed = 'move';
              }}
              {...dropProps(f.id)}
            >
              {/* Laid out like a note's gutter, so folder and note icons line up: the chevron
                  where a note's sub-note toggle is. */}
              <span className="note-gutter">
                <button type="button" className="note-sub-toggle" tabIndex={-1} aria-hidden="true" onClick={() => openFolder(f)}>
                  <ChevronIcon open={isOpen} />
                </button>
              </span>
              <button
                type="button"
                className="tree-toggle"
                aria-expanded={isOpen}
                onClick={() => openFolder(f)}
                onKeyDown={
                  canMove
                    ? shiftKeys(
                        f,
                        () => children.get(f.parentId ?? '') ?? [],
                        (target, where) => placeFolder(f.id, target, where),
                        `[data-folder="${CSS.escape(f.id)}"] .tree-toggle`,
                      )
                    : undefined
                }
              >
                <span className="tree-icon">
                  <FolderIcon open={isOpen} />
                </span>
                <span className="tree-name">{f.name}</span>
                {f.shared && (
                  <span className="note-row-shared" title={t(own ? 'sharing.badge' : 'sharing.folder.sharedWithYou')} aria-label={t(own ? 'sharing.badge' : 'sharing.folder.sharedWithYou')}>
                    <ShareIcon size={12} />
                  </span>
                )}
                <span className="tree-count">{counts.get(f.id) || ''}</span>
              </button>
              <span className="tree-actions">
                {onNewInFolder && canAdd && (
                  <button
                    type="button"
                    className="icon-button"
                    title={t('folders.newItem')}
                    aria-label={t('folders.newItemLabel', { name: f.name })}
                    onClick={() => {
                      openFolder(f, true);
                      onNewInFolder(f.id);
                    }}
                  >
                    <NewNoteIcon />
                  </button>
                )}
                <ShareFolder folder={f} />
                {canAdd && (
                  <button
                    type="button"
                    className="icon-button"
                    title={t('folders.newInside')}
                    aria-label={t('folders.newInsideLabel', { name: f.name })}
                    onClick={() => {
                      openFolder(f, true);
                      setEditing({ parentId: f.id, name: '' });
                    }}
                  >
                    <NewFolderIcon />
                  </button>
                )}
                {own && (
                  <>
                    <button
                      type="button"
                      className="icon-button"
                      title={t('folders.rename')}
                      aria-label={t('folders.renameLabel', { name: f.name })}
                      onClick={() => setEditing({ id: f.id, parentId: f.parentId ?? '', name: f.name })}
                    >
                      <PencilIcon />
                    </button>
                    {/* The folder of a paired reMarkable stays. */}
                    {!f.remarkable && (
                      <button
                        type="button"
                        className="icon-button danger"
                        title={t('common.delete')}
                        aria-label={t('folders.deleteLabel', { name: f.name })}
                        onClick={() => void remove(f)}
                      >
                        <TrashIcon />
                      </button>
                    )}
                  </>
                )}
              </span>
            </MenuDiv>
          )}
          {isOpen && (
            <ul className="tree-children">
              {editing && !editing.id && editing.parentId === f.id && <li>{nameField}</li>}
              {folderRows(f.id)}
              {noteRows(notesIn.get(f.id) ?? [])}
            </ul>
          )}
        </li>
      );
    });

  return (
    <div ref={treeRef} className={`folder-tree${dropTarget === '' ? ' drop' : ''}`} {...dropProps('')}>
      {error && <p className="error">{error}</p>}
      <ul className="conversation-list tree-root">
        {editing && !editing.id && editing.parentId === '' && <li>{nameField}</li>}
        {folderRows('')}
        {noteRows(notesIn.get('') ?? [])}
      </ul>
    </div>
  );
}
