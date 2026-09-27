import { Folder, Recording } from '../api/client';
import { title } from './recordings';

const byName = (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });

// childFolders maps each folder ID ('' for the top level) to its folders, by name.
export function childFolders(folders: Folder[]): Map<string, Folder[]> {
  const out = new Map<string, Folder[]>();
  for (const f of folders) {
    const key = f.parentId ?? '';
    out.set(key, [...(out.get(key) ?? []), f]);
  }
  for (const list of out.values()) list.sort(byName);
  return out;
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
