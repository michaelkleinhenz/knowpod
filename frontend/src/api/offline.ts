// Offline copies of the user's notes. API answers the app reads (the account, the notes
// list, labels, folders, each note) are kept in the browser's Cache Storage, and every note
// in the list is synced in the background, so the notes stay readable without a connection.
// The client answers reads from here when the server can't be reached; see request() in
// client.ts. Audio and document files are not kept. Everything is dropped on sign-out and
// when another user signs in.
import { useSyncExternalStore } from 'react';
import type { Recording } from './client';

const CACHE = 'knowpod-offline-v1';
// META holds the updatedAt of each note's kept copy, to find the notes that changed.
const META = '/offline/meta';
// A sync with more changed notes than this loads them all in one request.
const BULK_THRESHOLD = 20;
const PARALLEL = 4;

const available = typeof window !== 'undefined' && 'caches' in window;
const url = (path: string) => `/api/v1${path}`;

// Paths whose answers are kept. Everything else (admin pages, settings, models) needs a
// connection. The full notes list (full=1) is kept note by note, see syncNotes.
const KEPT = [/^\/auth\/me$/, /^\/recordings(\?(limit|number)=\d+)?$/, /^\/recordings\/[^/?]+$/, /^\/labels$/, /^\/folders$/, /^\/ai\/status$/, /^\/themes$/];
export const kept = (path: string) => KEPT.some((re) => re.test(path));

export const notePath = (id: string) => `/recordings/${encodeURIComponent(id)}`;

async function open(): Promise<Cache | null> {
  if (!available) return null;
  try {
    return await caches.open(CACHE);
  } catch {
    return null; // e.g. storage blocked
  }
}

export async function readOffline<T>(path: string): Promise<T | undefined> {
  const cache = await open();
  const res = await cache?.match(url(path));
  if (!res) return undefined;
  try {
    return (await res.json()) as T;
  } catch {
    return undefined;
  }
}

export async function writeOffline(path: string, data: unknown): Promise<void> {
  const cache = await open();
  try {
    await cache?.put(url(path), new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } }));
  } catch {
    // Quota exceeded or storage blocked: the app keeps working online.
  }
}

async function removeOffline(path: string): Promise<void> {
  const cache = await open();
  await cache?.delete(url(path));
}

// clearOffline drops all kept answers (sign-out, another user).
export async function clearOffline(): Promise<void> {
  meta = null;
  if (available) await caches.delete(CACHE).catch(() => undefined);
}

// --- Kept notes ---

let meta: Map<string, string> | null = null;

async function loadMeta(): Promise<Map<string, string>> {
  if (!meta) meta = new Map(Object.entries((await readOffline<Record<string, string>>(META)) ?? {}));
  return meta;
}

const saveMeta = (m: Map<string, string>) => writeOffline(META, Object.fromEntries(m));

const newer = (a: string | undefined, b: string | undefined) => !b || (!!a && Date.parse(a) >= Date.parse(b));

// keepNote stores a note as the server sent it, unless a newer copy is kept already (a
// background sync may answer after a save).
export async function keepNote(rec: Recording): Promise<void> {
  const m = await loadMeta();
  if (!newer(rec.updatedAt, m.get(rec.id))) return;
  m.set(rec.id, rec.updatedAt ?? '');
  await writeOffline(notePath(rec.id), rec);
  await saveMeta(m);
}

export async function forgetNote(id: string): Promise<void> {
  const m = await loadMeta();
  m.delete(id);
  await removeOffline(notePath(id));
  await saveMeta(m);
}

let syncing = false;

// syncNotes brings the kept copies up to date with the notes list (as the server sent it):
// changed and new notes are loaded, notes gone from the list are dropped. Notes still being
// processed (not ready) keep their last copy and are loaded once they are done.
export async function syncNotes(
  list: Recording[],
  ready: (r: Recording) => boolean,
  load: { one: (id: string) => Promise<Recording>; all: () => Promise<Recording[]> },
): Promise<void> {
  if (!available || syncing) return;
  syncing = true;
  try {
    const m = await loadMeta();
    const listed = new Set(list.map((r) => r.id));
    for (const id of [...m.keys()]) if (!listed.has(id)) await forgetNote(id);

    const stale = list.filter((r) => ready(r) && (!m.has(r.id) || m.get(r.id) !== (r.updatedAt ?? '')));
    if (stale.length === 0) return;
    if (stale.length > BULK_THRESHOLD) {
      for (const rec of await load.all()) await keepNote(rec);
      return;
    }
    const queue = stale.map((r) => r.id);
    await Promise.all(
      Array.from({ length: PARALLEL }, async () => {
        for (let id = queue.shift(); id; id = queue.shift()) {
          try {
            await keepNote(await load.one(id));
          } catch {
            // Tried again on the next sync.
          }
        }
      }),
    );
  } catch {
    // Offline or the server is unreachable: tried again on the next sync.
  } finally {
    syncing = false;
  }
}

// --- Connection state ---

// offline is true while the server can't be reached and the app shows kept copies.
let offline = typeof navigator !== 'undefined' && !navigator.onLine;
const listeners = new Set<() => void>();

export function setOffline(v: boolean) {
  if (v === offline) return;
  offline = v;
  listeners.forEach((l) => l());
}

export const isOffline = () => offline;

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// useOffline tells whether the server can't be reached right now.
export function useOffline(): boolean {
  return useSyncExternalStore(subscribe, isOffline);
}
