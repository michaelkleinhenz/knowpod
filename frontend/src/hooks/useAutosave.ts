import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, ApiError, Recording } from '../api/client';
import { errorText } from '../lib/errors';

// Changes are saved this long after the last change …
const AUTOSAVE_DELAY_MS = 2_000;
// … and at least this often while changes keep coming.
const AUTOSAVE_MAX_WAIT_MS = 10_000;
// A failed save is retried after this long (or as soon as the browser is back online).
const RETRY_MS = 10_000;

// conflict: someone else changed the text since it was loaded, and there are unsaved
// changes here; the user decides which version stays (resolve).
export type Sync = 'saved' | 'dirty' | 'saving' | 'error' | 'offline' | 'conflict';

// useAutosave keeps a note's summary (title and Markdown text) in sync with the server.
// The Markdown comes from the editor once it is ready (editorReady); until then nothing can
// change. Saves never overlap: a change made during a save is sent by the next one.
//
// Each save says which revision of the text it was made on (savedRevision at first, then
// the revision each save returns). When someone else saved the text in between, the server
// refuses it and the sync state becomes "conflict" rather than undoing their edit.
export function useAutosave(recordingId: string, savedTitle: string, savedRevision: number | undefined, onSaved: (rec: Recording) => void) {
  const { t } = useTranslation();
  const [title, setTitleState] = useState(savedTitle);
  const [sync, setSync] = useState<Sync>('saved');
  const [error, setError] = useState<string | null>(null);

  const titleRef = useRef(savedTitle);
  const getMarkdown = useRef<(() => string) | null>(null);
  // What the server has. The Markdown baseline is the editor's own output right after
  // loading, so normalizing the stored text never counts as a change.
  const baseline = useRef<{ title: string; markdown: string | null }>({ title: savedTitle, markdown: null });
  const inFlight = useRef<Promise<boolean> | null>(null);
  const discarded = useRef(false);
  // The revision of the text the editor holds; undefined sends no check.
  const revision = useRef(savedRevision);
  // Set while the user has to resolve a conflict; nothing is saved meanwhile.
  const conflicted = useRef(false);
  const timers = useRef<{ debounce?: number; maxWait?: number; retry?: number }>({});
  const onSavedRef = useRef(onSaved);
  onSavedRef.current = onSaved;

  const clearTimers = () => {
    window.clearTimeout(timers.current.debounce);
    window.clearTimeout(timers.current.maxWait);
    window.clearTimeout(timers.current.retry);
    timers.current = {};
  };

  const current = () => ({
    title: titleRef.current.trim(),
    markdown: getMarkdown.current ? getMarkdown.current() : baseline.current.markdown,
  });
  const isDirty = () => {
    if (baseline.current.markdown === null || discarded.current) return false;
    const c = current();
    return c.title !== baseline.current.title.trim() || c.markdown !== baseline.current.markdown;
  };

  const save = useCallback(async (overwrite = false): Promise<boolean> => {
    clearTimers();
    if (inFlight.current) {
      await inFlight.current;
    }
    if (conflicted.current && !overwrite) return false;
    if (!isDirty()) {
      if (!discarded.current) setSync('saved');
      return true;
    }
    const c = current();
    if (!c.title) {
      setSync('error');
      setError(t('editor.titleRequired'));
      return false;
    }
    setSync('saving');
    setError(null);
    const run = (async () => {
      try {
        const rec = await api.editSummary(recordingId, c.title, c.markdown ?? '', overwrite ? undefined : revision.current);
        baseline.current = { title: c.title, markdown: c.markdown };
        revision.current = rec.revision;
        conflicted.current = false;
        if (!discarded.current) onSavedRef.current(rec);
        return true;
      } catch (err) {
        if (discarded.current) return false;
        if (err instanceof ApiError && err.code === 'changed') {
          conflicted.current = true;
          setSync('conflict');
          setError(null);
          return false;
        }
        const offline = !navigator.onLine;
        setSync(offline ? 'offline' : 'error');
        setError(offline ? null : errorText(err, t));
        timers.current.retry = window.setTimeout(() => void save(), RETRY_MS);
        return false;
      }
    })();
    inFlight.current = run;
    const ok = await run;
    inFlight.current = null;
    if (!ok || conflicted.current) return false;
    if (isDirty()) {
      setSync('dirty');
      schedule(); // changed during the save
      return false;
    }
    setSync('saved');
    return true;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recordingId, t]);

  const schedule = useCallback(() => {
    window.clearTimeout(timers.current.debounce);
    timers.current.debounce = window.setTimeout(() => void save(), AUTOSAVE_DELAY_MS);
    if (timers.current.maxWait === undefined) timers.current.maxWait = window.setTimeout(() => void save(), AUTOSAVE_MAX_WAIT_MS);
  }, [save]);

  // changed is called on every edit.
  const changed = useCallback(() => {
    if (conflicted.current) return;
    if (!isDirty()) {
      if (!inFlight.current) setSync('saved');
      return;
    }
    setSync((s) => (s === 'saving' ? s : 'dirty'));
    schedule();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schedule]);

  const setTitle = useCallback(
    (v: string) => {
      titleRef.current = v;
      setTitleState(v);
      changed();
    },
    [changed],
  );

  // editorReady connects the editor; its current output becomes the unchanged baseline.
  const editorReady = useCallback((get: () => string) => {
    getMarkdown.current = get;
    baseline.current = { title: baseline.current.title, markdown: get() };
  }, []);

  // discard stops all saving, e.g. before the summary is regenerated (after the user
  // confirmed that edits may be lost).
  const discard = useCallback(() => {
    discarded.current = true;
    clearTimers();
  }, []);

  // dirty says whether there are changes here that aren't saved yet; settled waits for a
  // save that is under way.
  const dirty = useCallback(() => isDirty(), []);
  const settled = useCallback(async () => {
    await inFlight.current;
  }, []);
  // known says whether the editor holds this revision of the text (it is ours).
  const known = useCallback((rev: number | undefined) => rev === undefined || rev === revision.current, []);

  // conflict marks that someone else changed the text while there are unsaved changes here.
  const conflict = useCallback(() => {
    conflicted.current = true;
    clearTimers();
    setSync('conflict');
  }, []);

  // keepMine saves the text here over the other version.
  const keepMine = useCallback(() => {
    conflicted.current = false;
    return save(true);
  }, [save]);

  // Retry when the connection comes back; ask before closing the tab with unsaved changes;
  // save what's left when the note is left.
  useEffect(() => {
    const online = () => isDirty() && !conflicted.current && void save();
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (isDirty() || inFlight.current) e.preventDefault();
    };
    window.addEventListener('online', online);
    window.addEventListener('beforeunload', beforeUnload);
    return () => {
      window.removeEventListener('online', online);
      window.removeEventListener('beforeunload', beforeUnload);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [save]);
  useEffect(
    () => () => {
      if (isDirty() && !conflicted.current) void save();
      clearTimers();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // markdown is the text as it is now, unsaved changes included (null before the editor is ready).
  const markdown = useCallback(() => current().markdown, []);

  return { title, setTitle, sync, error, changed, editorReady, save: () => save(), discard, dirty, settled, known, conflict, keepMine, markdown };
}
