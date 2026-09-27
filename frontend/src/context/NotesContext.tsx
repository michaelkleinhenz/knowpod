import { createContext, ReactNode, useCallback, useContext, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Folder, Label, Recording, RECORDINGS_LIMIT } from '../api/client';
import { isOffline, syncNotes, useOffline, writeOffline } from '../api/offline';
import { errorText } from '../lib/errors';
import { processing } from '../lib/recordings';

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
  aiReady: boolean;
  error: string | null;
  refreshing: boolean;
  reload: () => Promise<void>;
  // upsert puts a changed recording into the list (e.g. after the open note was saved).
  upsert: (rec: Recording) => void;
  remove: (id: string) => void;
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
  const [aiReady, setAIReady] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const reload = useCallback(async () => {
    setRefreshing(true);
    try {
      const [list, ai] = await Promise.all([api.recordings(), api.aiStatus()]);
      setRecordings(list);
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

  useEffect(() => {
    void reload();
    void reloadLabels();
    void reloadFolders();
  }, [reload, reloadLabels, reloadFolders]);

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
    };
    window.addEventListener('online', online);
    const timer = offline ? setInterval(online, OFFLINE_RETRY_MS) : undefined;
    return () => {
      window.removeEventListener('online', online);
      clearInterval(timer);
    };
  }, [offline, reload, reloadLabels, reloadFolders]);

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

  return (
    <NotesContext.Provider value={{ recordings, labels, reloadLabels, folders, reloadFolders, aiReady, error, refreshing, reload, upsert, remove }}>{children}</NotesContext.Provider>
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
