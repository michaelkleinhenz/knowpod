import { useSyncExternalStore } from 'react';

// The folders and notes whose contents are shown (expanded), shared by the sidebar's folder
// view and the sub-notes of the open note, and remembered in the browser.
const OPEN_KEY = 'knowpod.openFolders';

function load(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(OPEN_KEY) ?? '[]') as string[]);
  } catch {
    return new Set();
  }
}

let open = load();
const listeners = new Set<() => void>();

function set(next: Set<string>) {
  open = next;
  try {
    localStorage.setItem(OPEN_KEY, JSON.stringify([...open]));
  } catch {
    // Private mode or storage full: the trees just start collapsed next time.
  }
  listeners.forEach((l) => l());
}

// setOpen expands (on) or collapses the folders or notes with these IDs.
export function setOpen(ids: string[], on: boolean) {
  const next = new Set(open);
  for (const id of ids) {
    if (on) next.add(id);
    else next.delete(id);
  }
  if (next.size !== open.size || ids.some((id) => next.has(id) !== open.has(id))) set(next);
}

// useOpen returns the expanded folders and notes; it changes whenever one opens or closes.
export function useOpen(): Set<string> {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => open,
  );
}
