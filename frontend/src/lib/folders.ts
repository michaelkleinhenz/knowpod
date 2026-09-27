import { Folder, Recording } from '../api/client';
import { title } from './recordings';

const compareText = (a: string, b: string) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true });

// byPosition orders items the user put in order (position 1 up) first, then the others
// (no position) by text.
function byPosition<T extends { position?: number }>(text: (x: T) => string) {
  return (a: T, b: T) => (a.position || Infinity) - (b.position || Infinity) || compareText(text(a), text(b));
}
const folderOrder = byPosition<Folder>((f) => f.name);
const noteOrder = byPosition<Recording>(title);

// childFolders maps each folder ID ('' for the top level) to its folders, in their order.
export function childFolders(folders: Folder[]): Map<string, Folder[]> {
  const out = new Map<string, Folder[]>();
  for (const f of folders) {
    const key = f.parentId ?? '';
    out.set(key, [...(out.get(key) ?? []), f]);
  }
  for (const list of out.values()) list.sort(folderOrder);
  return out;
}

// subNotes maps each note ID to its sub-notes, in their order. Notes whose parent is unknown
// (e.g. deleted meanwhile) are left out; they are shown in their folder instead.
export function subNotes(notes: Recording[]): Map<string, Recording[]> {
  const ids = new Set(notes.map((r) => r.id));
  const out = new Map<string, Recording[]>();
  for (const r of notes) {
    if (r.parentId && ids.has(r.parentId)) out.set(r.parentId, [...(out.get(r.parentId) ?? []), r]);
  }
  for (const [k, list] of out) out.set(k, sortInPlace(list));
  return out;
}

// withSubNotes lists note id and all notes below it that have sub-notes of their own (the
// notes that open and close), to open or close a whole tree at once.
export function withSubNotes(id: string, subs: Map<string, Recording[]>): string[] {
  const out: string[] = [];
  const walk = (n: string) => {
    if (out.includes(n) || !subs.has(n) || out.length > 1000) return;
    out.push(n);
    for (const c of subs.get(n) ?? []) walk(c.id);
  };
  walk(id);
  return out;
}

// parentOf returns the note a note is shown under, if it is a sub-note of a known note.
export function parentOf(r: Recording, byId: Map<string, Recording>): Recording | undefined {
  return r.parentId ? byId.get(r.parentId) : undefined;
}

// notePath lists the notes above r, from the topmost down to its parent.
export function notePath(r: Recording, notes: Recording[] | null): Recording[] {
  const byId = new Map((notes ?? []).map((n) => [n.id, n]));
  const out: Recording[] = [];
  for (let p = parentOf(r, byId); p && out.length < 16 && p.id !== r.id; p = parentOf(p, byId)) out.unshift(p);
  return out;
}

// isUnderNote reports whether note id is target or one of target's sub-notes (at any
// depth), i.e. whether putting target under id would put it under itself.
export function isUnderNote(id: string, target: string, notes: Recording[]): boolean {
  const byId = new Map(notes.map((n) => [n.id, n]));
  let n = 0;
  for (let r = byId.get(id); r && n < 64; r = parentOf(r, byId), n++) {
    if (r.id === target) return true;
  }
  return false;
}

// folderOf returns the folder a note is shown in: its folder, or the top level ('') when
// it has none or the folder is unknown (e.g. deleted meanwhile).
export function folderOf(r: Recording, ids: Set<string>): string {
  return r.folderId && ids.has(r.folderId) ? r.folderId : '';
}

// sortByTitle sorts notes by title, like files in a folder.
export function sortByTitle(list: Recording[]): Recording[] {
  return list.slice().sort((a, b) => title(a).localeCompare(title(b), undefined, { sensitivity: 'base', numeric: true }));
}

// sortInPlace sorts the notes in one place (a folder or under a note) in the order the user
// put them, the unordered ones last, by title.
export function sortInPlace(list: Recording[]): Recording[] {
  return list.slice().sort(noteOrder);
}

// flatTree lists the folders depth-first, each with its nesting depth (0 at the top level).
export function flatTree(folders: Folder[]): { folder: Folder; depth: number }[] {
  const children = childFolders(folders);
  const out: { folder: Folder; depth: number }[] = [];
  const walk = (parent: string, depth: number) => {
    for (const f of children.get(parent) ?? []) {
      out.push({ folder: f, depth });
      walk(f.id, depth + 1);
    }
  };
  walk('', 0);
  return out;
}

// folderPath names the folders from the top level down to id.
export function folderPath(id: string | undefined, folders: Folder[] | null): string[] {
  const byId = new Map((folders ?? []).map((f) => [f.id, f]));
  const out: string[] = [];
  for (let f = id ? byId.get(id) : undefined; f && out.length < 16; f = f.parentId ? byId.get(f.parentId) : undefined) out.unshift(f.name);
  return out;
}

// isInside reports whether folder id is target or one of target's folders (at any depth),
// i.e. whether moving target into id would put it inside itself.
export function isInside(id: string, target: string, folders: Folder[]): boolean {
  const byId = new Map(folders.map((f) => [f.id, f]));
  for (let f = byId.get(id); f; f = f.parentId ? byId.get(f.parentId) : undefined) {
    if (f.id === target) return true;
  }
  return false;
}
