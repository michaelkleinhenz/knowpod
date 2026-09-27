import { DragEvent, FormEvent, ReactNode, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Folder, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { errorText } from '../lib/errors';
import { childFolders, folderOf, isInside, isUnderNote, sortByTitle, subNotes } from '../lib/folders';
import { formatDate, when } from '../lib/recordings';
import { ChevronIcon, FolderIcon, NewFolderIcon, PencilIcon, TrashIcon } from './Icons';
import { NoteRow } from './NoteRow';

// Drag data types; the browser only reveals the types (not the data) while dragging over.
const NOTE_TYPE = 'application/x-knowpod-note';
const FOLDER_TYPE = 'application/x-knowpod-folder';
const OPEN_KEY = 'knowpod.openFolders';
const NO_FOLDERS: Folder[] = [];

// Editing is the inline name field: renaming folder id, or a new folder in parentId.
type Editing = { id?: string; parentId: string; name: string };

function loadOpen(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(OPEN_KEY) ?? '[]') as string[]);
  } catch {
    return new Set();
  }
}

function dragged(e: DragEvent): 'note' | 'folder' | null {
  const types = e.dataTransfer.types;
  return types.includes(NOTE_TYPE) ? 'note' : types.includes(FOLDER_TYPE) ? 'folder' : null;
}

interface Props {
  notes: Recording[];
  query: string;
  activeId?: string;
  aiReady: boolean;
  onSetDone: (r: Recording, done: boolean) => void;
  // newFolder is bumped by the list's "New folder" button to start a folder at the top level.
  newFolder: number;
}

// FolderTree shows the notes in their folders, like files. Folders can be created, renamed,
// deleted and nested; notes and folders are moved by dragging them onto a folder (or onto
// the free space below, for the top level). Notes with sub-notes open like folders; a note
// dropped onto another note becomes its sub-note.
export function FolderTree({ notes, query, activeId, aiReady, onSetDone, newFolder }: Props) {
  const { t } = useTranslation();
  const { folders, recordings, reloadFolders, reload, upsert } = useNotes();
  const [open, setOpen] = useState(loadOpen);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const saving = useRef(false);

  useEffect(() => {
    try {
      localStorage.setItem(OPEN_KEY, JSON.stringify([...open]));
    } catch {
      // Private mode or storage full: the tree just starts collapsed next time.
    }
  }, [open]);

  // Before paint, so the name field is there (and focused) for the first key typed.
  useLayoutEffect(() => {
    if (newFolder) setEditing({ parentId: '', name: '' });
  }, [newFolder]);

  const all = folders ?? NO_FOLDERS;
  // The tree is built from all notes, so that sub-notes found by a search show under their
  // parents; notes lists the ones to show.
  const allNotes = recordings ?? notes;
  const searching = query.trim() !== '';
  const { children, notesIn, subs, counts, shown } = useMemo(() => {
    const ids = new Set(all.map((f) => f.id));
    const subs = subNotes(allNotes);
    const withParent = new Set([...subs.values()].flat().map((r) => r.id));
    // notesIn are the notes directly in each folder; sub-notes are under their parents.
    const notesIn = new Map<string, Recording[]>();
    for (const r of allNotes) {
      if (withParent.has(r.id)) continue;
      const key = folderOf(r, ids);
      notesIn.set(key, [...(notesIn.get(key) ?? []), r]);
    }
    for (const [k, list] of notesIn) notesIn.set(k, sortByTitle(list));
    // shown counts the listed notes in each note, itself and its sub-notes at any depth;
    // notes counting 0 are hidden.
    const listed = new Set(notes.map((r) => r.id));
    const shown = new Map<string, number>();
    const countNote = (r: Recording): number => {
      const n = (listed.has(r.id) ? 1 : 0) + (subs.get(r.id) ?? []).reduce((s, c) => s + countNote(c), 0);
      shown.set(r.id, n);
      return n;
    };
    const children = childFolders(all);
    // counts are the notes in each folder, including its folders and sub-notes.
    const counts = new Map<string, number>();
    const count = (id: string): number => {
      const n = (notesIn.get(id) ?? []).reduce((s, r) => s + countNote(r), 0) + (children.get(id) ?? []).reduce((s, f) => s + count(f.id), 0);
      counts.set(id, n);
      return n;
    };
    count('');
    return { children, notesIn, subs, counts, shown };
  }, [all, allNotes, notes]);

  const toggle = (id: string, on = !open.has(id)) =>
    setOpen((s) => {
      const next = new Set(s);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

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

  async function moveNote(id: string, folderId: string) {
    const r = allNotes.find((n) => n.id === id);
    if (!r || ((r.folderId ?? '') === folderId && !r.parentId)) return;
    upsert({ ...r, folderId: folderId || undefined, parentId: undefined });
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
    upsert({ ...r, parentId, folderId: undefined });
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

  async function moveFolder(id: string, parentId: string) {
    const f = all.find((x) => x.id === id);
    if (!f || (f.parentId ?? '') === parentId || (parentId && isInside(parentId, id, all))) return;
    await run(async () => {
      await api.updateFolder(id, { name: f.name, parentId: parentId || undefined });
      await reloadFolders();
    });
  }

  // Drop handlers for a folder (id) or the top level ('').
  const dropProps = (id: string) => ({
    onDragOver: (e: DragEvent) => {
      if (!dragged(e)) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = 'move';
      setDropTarget(id);
    },
    onDragLeave: (e: DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropTarget((d) => (d === id ? null : d));
    },
    onDrop: (e: DragEvent) => {
      const kind = dragged(e);
      if (!kind) return;
      e.preventDefault();
      e.stopPropagation();
      setDropTarget(null);
      const item = e.dataTransfer.getData(kind === 'note' ? NOTE_TYPE : FOLDER_TYPE);
      if (kind === 'note') void moveNote(item, id);
      else if (item !== id) void moveFolder(item, id);
      if (id) toggle(id, true);
    },
  });

  // Drop handlers for a note: notes dropped onto it become its sub-notes.
  const noteDropProps = (id: string) => {
    const key = `note:${id}`;
    return {
      onDragOver: (e: DragEvent) => {
        if (dragged(e) !== 'note') return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropTarget(key);
      },
      onDragLeave: (e: DragEvent) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropTarget((d) => (d === key ? null : d));
      },
      onDrop: (e: DragEvent) => {
        if (dragged(e) !== 'note') return;
        e.preventDefault();
        e.stopPropagation();
        setDropTarget(null);
        void moveUnder(e.dataTransfer.getData(NOTE_TYPE), id);
      },
    };
  };

  const nameField = (
    <form className="tree-rename" onSubmit={saveName}>
      <FolderIcon />
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
  const noteRows = (list: Recording[]): ReactNode =>
    list.map((r) => {
      if (!shown.get(r.id)) return null;
      const kids = (subs.get(r.id) ?? []).filter((c) => shown.get(c.id));
      const isOpen = searching || open.has(r.id);
      return (
        <NoteRow
          key={r.id}
          rec={r}
          active={r.id === activeId}
          aiReady={aiReady}
          meta={formatDate(when(r))}
          onSetDone={onSetDone}
          onDragStart={(e) => {
            e.dataTransfer.setData(NOTE_TYPE, r.id);
            e.dataTransfer.effectAllowed = 'move';
          }}
          sub={kids.length > 0 ? { count: kids.length, open: isOpen, onToggle: () => toggle(r.id) } : undefined}
          lineProps={noteDropProps(r.id)}
          drop={dropTarget === `note:${r.id}`}
        >
          {kids.length > 0 && isOpen && <ul className="tree-children sub-note-list">{noteRows(kids)}</ul>}
        </NoteRow>
      );
    });

  const folderRows = (parent: string) =>
    (children.get(parent) ?? []).map((f) => {
      // While searching, only folders with matches are shown, all of them open.
      if (searching && !counts.get(f.id)) return null;
      const isOpen = searching || open.has(f.id);
      const renaming = editing?.id === f.id;
      return (
        <li key={f.id} className="tree-folder">
          {renaming ? (
            nameField
          ) : (
            <div
              className={`tree-row${dropTarget === f.id ? ' drop' : ''}`}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData(FOLDER_TYPE, f.id);
                e.dataTransfer.effectAllowed = 'move';
              }}
              {...dropProps(f.id)}
            >
              <button type="button" className="tree-toggle" aria-expanded={isOpen} onClick={() => toggle(f.id)}>
                <ChevronIcon open={isOpen} />
                <FolderIcon open={isOpen} />
                <span className="tree-name">{f.name}</span>
                <span className="tree-count">{counts.get(f.id) || ''}</span>
              </button>
              <span className="tree-actions">
                <button
                  type="button"
                  className="icon-button"
                  title={t('folders.newInside')}
                  aria-label={t('folders.newInsideLabel', { name: f.name })}
                  onClick={() => {
                    toggle(f.id, true);
                    setEditing({ parentId: f.id, name: '' });
                  }}
                >
                  <NewFolderIcon />
                </button>
                <button
                  type="button"
                  className="icon-button"
                  title={t('folders.rename')}
                  aria-label={t('folders.renameLabel', { name: f.name })}
                  onClick={() => setEditing({ id: f.id, parentId: f.parentId ?? '', name: f.name })}
                >
                  <PencilIcon />
                </button>
                <button
                  type="button"
                  className="icon-button danger"
                  title={t('common.delete')}
                  aria-label={t('folders.deleteLabel', { name: f.name })}
                  onClick={() => void remove(f)}
                >
                  <TrashIcon />
                </button>
              </span>
            </div>
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
    <div className={`folder-tree${dropTarget === '' ? ' drop' : ''}`} {...dropProps('')}>
      {error && <p className="error">{error}</p>}
      <ul className="conversation-list tree-root">
        {editing && !editing.id && editing.parentId === '' && <li>{nameField}</li>}
        {folderRows('')}
        {noteRows(notesIn.get('') ?? [])}
      </ul>
      {!searching && folders && folders.length === 0 && !editing && <p className="muted tree-hint">{t('folders.empty')}</p>}
      {!searching && folders && folders.length > 0 && <p className="muted tree-hint">{t('folders.dragHint')}</p>}
    </div>
  );
}
