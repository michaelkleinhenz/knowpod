import { createContext, ReactNode, useCallback, useContext, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, Label, Recording } from '../api/client';
import { errorText } from '../lib/errors';
import { processing } from '../lib/recordings';

const POLL_MS = 10_000;

interface NotesState {
  recordings: Recording[] | null;
  // labels are the user's labels (built-in first), shared by the list and the open note.
  labels: Label[] | null;
  reloadLabels: () => Promise<void>;
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
// it fresh while any note is still being processed.
export function NotesProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [recordings, setRecordings] = useState<Recording[] | null>(null);
  const [labels, setLabels] = useState<Label[] | null>(null);
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

  useEffect(() => {
    void reload();
    void reloadLabels();
  }, [reload, reloadLabels]);

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
    <NotesContext.Provider value={{ recordings, labels, reloadLabels, aiReady, error, refreshing, reload, upsert, remove }}>{children}</NotesContext.Provider>
  );
}

export function useNotes(): NotesState {
  const ctx = useContext(NotesContext);
  if (!ctx) throw new Error('useNotes must be used inside NotesProvider');
  return ctx;
}
