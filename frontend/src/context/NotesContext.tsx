import { createContext, ReactNode, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, ApiError, Folder, Label, NoteEvent, Recording, RECORDINGS_LIMIT, SavedFilter, TimeEntry } from '../api/client';
import { forgetNote, isOffline, syncNotes, useOffline, writeOffline } from '../api/offline';
import { errorText } from '../lib/errors';
import { FilterContext } from '../lib/filterQuery';
import { processing } from '../lib/recordings';
import { notifyFocusDone } from '../lib/timer';

const POLL_MS = 10_000;
// While the server can't be reached, it is tried again this often.
const OFFLINE_RETRY_MS = 30_000;

const loadNote = { one: api.recording, all: api.allRecordings };

interface NotesState {
  recordings: Recording[] | null;
  // labels are the user's labels (built-in first), shared by the list and the open note.
  labels: Label[] | null;
  reloadLabels: () => Promise<void>;
  // folders are the user's folders, shared by the folder view and the note's move menu.
  folders: Folder[] | null;
  reloadFolders: () => Promise<void>;
  // filters are the user's saved filters, pinned in the list and shown by boards.
  filters: SavedFilter[] | null;
  reloadFilters: () => Promise<void>;
  // filterContext resolves label and folder names in filter queries.
  filterContext: FilterContext;
  // timer is the running timer; starting one stops the one that was running.
  timer: TimeEntry | null;
  startTimer: (noteId: string, minutes?: number) => Promise<void>;
  stopTimer: () => Promise<void>;
  aiReady: boolean;
  error: string | null;
  refreshing: boolean;
  reload: () => Promise<void>;
  // upsert puts a changed recording into the list (e.g. after the open note was saved).
  upsert: (rec: Recording) => void;
  remove: (id: string) => void;
  // trash holds the notes in the trash, newest deletion first.
  trash: Recording[] | null;
  // moveToTrash, restore, deleteForever and emptyTrash change the trash and the list alike.
  moveToTrash: (rec: Recording) => Promise<void>;
  restore: (rec: Recording) => Promise<Recording>;
  deleteForever: (rec: Recording) => Promise<void>;
  emptyTrash: () => Promise<void>;
}

// byDeletion sorts notes in the trash by when they were deleted, newest first.
function byDeletion(list: Recording[]): Recording[] {
  return list.slice().sort((a, b) => (b.deletedAt ?? '').localeCompare(a.deletedAt ?? ''));
}

const NotesContext = createContext<NotesState | null>(null);

// NotesProvider holds the list of notes shared by the sidebar and the open note, and keeps
// it fresh while any note is still being processed. Each load also brings the notes kept
// for offline reading up to date; offline, the kept list is shown.
export function NotesProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [recordings, setRecordings] = useState<Recording[] | null>(null);
  const [labels, setLabels] = useState<Label[] | null>(null);
  const [folders, setFolders] = useState<Folder[] | null>(null);
  const [filters, setFilters] = useState<SavedFilter[] | null>(null);
  const [timer, setTimer] = useState<TimeEntry | null>(null);
  const [aiReady, setAIReady] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [trash, setTrash] = useState<Recording[] | null>(null);

  const reload = useCallback(async () => {
    setRefreshing(true);
    try {
      const [list, ai, trashed] = await Promise.all([api.recordings(), api.aiStatus(), api.trash().catch(() => null)]);
      setRecordings(list);
      if (trashed) setTrash(byDeletion(trashed));
      setAIReady(ai.transcription && ai.summary);
      setError(null);
      if (!isOffline()) void syncNotes(list, (r) => !processing(r), loadNote);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setRefreshing(false);
    }
  }, [t]);

  const reloadLabels = useCallback(async () => {
    try {
      setLabels(await api.labels());
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [t]);

  const reloadFolders = useCallback(async () => {
    try {
      setFolders(await api.folders());
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [t]);

  const reloadFilters = useCallback(async () => {
    try {
      setFilters(await api.filters());
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [t]);

  const reloadTimer = useCallback(async () => {
    try {
      setTimer((await api.timer()).timer);
    } catch {
      // Offline: the timer keeps running on the server and shows up once back.
    }
  }, []);

  useEffect(() => {
    void reload();
    void reloadLabels();
    void reloadFolders();
    void reloadFilters();
    void reloadTimer();
  }, [reload, reloadLabels, reloadFolders, reloadFilters, reloadTimer]);

  // Keep the list as shown (with saved changes) for offline reading.
  useEffect(() => {
    if (recordings) void writeOffline(`/recordings?limit=${RECORDINGS_LIMIT}`, recordings);
  }, [recordings]);

  // Offline: reload when the browser is back online, and every so often in case only the
  // server was unreachable.
  const offline = useOffline();
  useEffect(() => {
    const online = () => {
      void reload();
      void reloadLabels();
      void reloadFolders();
      void reloadFilters();
      void reloadTimer();
    };
    window.addEventListener('online', online);
    const timer = offline ? setInterval(online, OFFLINE_RETRY_MS) : undefined;
    return () => {
      window.removeEventListener('online', online);
      clearInterval(timer);
    };
  }, [offline, reload, reloadLabels, reloadFolders, reloadFilters, reloadTimer]);

  const busy = recordings?.some(processing) ?? false;
  useEffect(() => {
    if (!busy) return;
    const timer = setInterval(() => void reload(), POLL_MS);
    return () => clearInterval(timer);
  }, [busy, reload]);

  const upsert = useCallback((rec: Recording) => {
    setRecordings((list) => {
      if (!list) return list;
      const i = list.findIndex((r) => r.id === rec.id);
      if (i < 0) return [rec, ...list];
      const next = list.slice();
      next[i] = rec;
      return next;
    });
  }, []);

  const remove = useCallback((id: string) => {
    setRecordings((list) => list?.filter((r) => r.id !== id) ?? list);
  }, []);

  // Live updates: changes of the notes the user sees, made by anyone (people the notes are
  // shared with, the user in another window, the processing), arrive as events. A changed
  // note is loaded again unless the list has that version already; one that can't be
  // loaded any more is gone for the user (deleted, or no longer shared).
  const listRef = useRef(recordings);
  listRef.current = recordings;
  const refetch = useCallback(
    async (id: string) => {
      try {
        const rec = await api.recording(id);
        if (rec.deletedAt) {
          remove(id);
          void reload(); // the trash changed
        } else upsert(rec);
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          remove(id);
          void forgetNote(id);
        }
      }
    },
    [remove, upsert, reload],
  );
  useEffect(() => {
    if (typeof EventSource === 'undefined') return;
    const source = new EventSource(api.eventsURL, { withCredentials: true });
    let opened = false;
    source.addEventListener('open', () => {
      // After a reconnect, changes made meanwhile were missed.
      if (opened) void reload();
      opened = true;
    });
    source.addEventListener('note', (e) => {
      const ev = JSON.parse((e as MessageEvent<string>).data) as NoteEvent;
      if (!ev.id) return;
      const known = listRef.current?.find((r) => r.id === ev.id);
      if (known && ev.version && (known.version ?? 0) >= ev.version) return;
      void refetch(ev.id);
    });
    // Many notes changed, e.g. a folder was shared with the user or no longer is: the notes
    // and the folders are loaded again.
    source.addEventListener('reload', () => {
      void reload();
      void reloadFolders();
    });
    return () => source.close();
  }, [refetch, reload, reloadFolders]);

  const moveToTrash = useCallback(
    async (rec: Recording) => {
      const trashed = await api.trashNote(rec.id);
      remove(rec.id);
      setTrash((list) => byDeletion([trashed, ...(list ?? []).filter((r) => r.id !== rec.id)]));
      // Its sub-notes moved up to where it was.
      if (recordings?.some((r) => r.parentId === rec.id)) void reload();
    },
    [recordings, reload, remove],
  );

  const restore = useCallback(
    async (rec: Recording) => {
      const back = await api.restoreNote(rec.id);
      setTrash((list) => list?.filter((r) => r.id !== rec.id) ?? list);
      upsert(back);
      return back;
    },
    [upsert],
  );

  const deleteForever = useCallback(
    async (rec: Recording) => {
      await api.deleteNote(rec.id);
      setTrash((list) => list?.filter((r) => r.id !== rec.id) ?? list);
      if (!rec.deletedAt) {
        remove(rec.id);
        if (recordings?.some((r) => r.parentId === rec.id)) void reload();
      }
    },
    [recordings, reload, remove],
  );

  // refreshNote loads a note in the list again, e.g. after time was logged on it.
  const refreshNote = useCallback(async (id: string) => {
    try {
      const rec = await api.recording(id);
      setRecordings((list) => list?.map((r) => (r.id === rec.id ? rec : r)) ?? list);
    } catch {
      // Deleted meanwhile or offline; the next reload brings it up to date.
    }
  }, []);

  const startTimer = useCallback(
    async (noteId: string, minutes?: number) => {
      const before = timer;
      const { timer: next } = await api.startTimer(noteId, minutes);
      setTimer(next);
      if (before) void refreshNote(before.noteId);
    },
    [timer, refreshNote],
  );

  const stopTimer = useCallback(async () => {
    const { stopped } = await api.stopTimer();
    setTimer(null);
    if (stopped) void refreshNote(stopped.noteId);
  }, [refreshNote]);

  // A focus session stops by itself on the server; the app follows at the same moment.
  useEffect(() => {
    if (!timer?.until) return;
    const ends = new Date(timer.until).getTime() - Date.now();
    const done = setTimeout(
      () => {
        notifyFocusDone(timer.noteTitle, t);
        setTimer(null);
        void refreshNote(timer.noteId);
      },
      Math.max(ends, 0) + 500,
    );
    return () => clearTimeout(done);
  }, [timer, refreshNote, t]);

  const filterContext = useMemo<FilterContext>(() => ({ labels: labels ?? [], folders: folders ?? [], notes: recordings ?? [] }), [labels, folders, recordings]);

  const emptyTrash = useCallback(async () => {
    const gone = trash ?? [];
    await api.emptyTrash();
    setTrash([]);
    await Promise.all(gone.map((r) => forgetNote(r.id)));
  }, [trash]);

  return (
    <NotesContext.Provider
      value={{
        recordings,
        labels,
        reloadLabels,
        folders,
        reloadFolders,
        filters,
        reloadFilters,
        filterContext,
        timer,
        startTimer,
        stopTimer,
        aiReady,
        error,
        refreshing,
        reload,
        upsert,
        remove,
        trash,
        moveToTrash,
        restore,
        deleteForever,
        emptyTrash,
      }}
    >
      {children}
    </NotesContext.Provider>
  );
}

export function useNotes(): NotesState {
  const ctx = useContext(NotesContext);
  if (!ctx) throw new Error('useNotes must be used inside NotesProvider');
  return ctx;
}

// useNotesIfAny returns the notes state, or null outside the notes pages.
export function useNotesIfAny(): NotesState | null {
  return useContext(NotesContext);
}
