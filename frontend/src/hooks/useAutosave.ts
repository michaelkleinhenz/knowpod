import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api, ApiError, Recording, SIGNED_IN_EVENT } from '../api/client';
import { useAuth } from '../auth';
import { Draft, readDraft, removeDraft, writeDraft } from '../lib/drafts';
import { errorText } from '../lib/errors';

// Changes are saved this long after the last change …
const AUTOSAVE_DELAY_MS = 2_000;
// … and at least this often while changes keep coming.
const AUTOSAVE_MAX_WAIT_MS = 10_000;
// A failed save is retried after this long (or as soon as the browser is back online, or
// signed in again).
const RETRY_MS = 10_000;
// Unsaved edits are kept as a draft (lib/drafts.ts) this long after the last change.
const DRAFT_DELAY_MS = 300;

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
//
// Unsaved edits are also kept in the browser as a draft until the server has them. When the
// note is opened with a draft (a save didn't get through before the app was closed, or the
// session ended), restored holds it: the editor starts on its text (instead of savedMarkdown)
// and it is saved like any edit, on the revision it was made on.
export function useAutosave(
  recordingId: string,
  savedTitle: string,
  savedRevision: number | undefined,
  onSaved: (rec: Recording) => void,
  savedMarkdown?: string,
  // keepDrafts: unsaved edits may be kept and restored (not for read-only text).
  keepDrafts = true,
) {
  const { t } = useTranslation();
  const userId = useAuth().account?.id;
  // The draft found when the note was opened.
  const [restored] = useState<Draft | null>(() => (keepDrafts ? readDraft(recordingId, userId) : null));
  const [title, setTitleState] = useState(restored?.title ?? savedTitle);
  const [sync, setSync] = useState<Sync>('saved');
  const [error, setError] = useState<string | null>(null);

  const titleRef = useRef(restored?.title ?? savedTitle);
  const getMarkdown = useRef<(() => string) | null>(null);
  // What the server has. The Markdown baseline is the editor's own output right after
  // loading, so normalizing the stored text never counts as a change.
  const baseline = useRef<{ title: string; markdown: string | null }>({ title: savedTitle, markdown: null });
  const inFlight = useRef<Promise<boolean> | null>(null);
  const discarded = useRef(false);
  // The revision of the text the editor holds; undefined sends no check.
  const revision = useRef(restored ? restored.revision : savedRevision);
  // The revision stored when the note was opened: not someone else's change, even when the
  // editor holds a draft made on an earlier one.
  const openedRevision = useRef(savedRevision);
  // Set while the user has to resolve a conflict; nothing is saved meanwhile.
  const conflicted = useRef(false);
  const timers = useRef<{ debounce?: number; maxWait?: number; retry?: number }>({});
  const draftTimer = useRef<number | undefined>(undefined);
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

  // keepDraft keeps the unsaved edits in the browser, or forgets them once there are none.
  const keepDraft = () => {
    window.clearTimeout(draftTimer.current);
    draftTimer.current = undefined;
    if (!keepDrafts || !userId || discarded.current) return;
    if (!isDirty()) {
      removeDraft(recordingId);
      return;
    }
    const c = current();
    writeDraft(recordingId, { userId, title: titleRef.current, markdown: c.markdown ?? '', revision: revision.current });
  };
  const scheduleDraft = () => {
    window.clearTimeout(draftTimer.current);
    draftTimer.current = window.setTimeout(keepDraft, DRAFT_DELAY_MS);
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
        keepDraft(); // forgotten, unless changed during the save
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
        keepDraft(); // in case the app is closed before a retry gets through
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
    scheduleDraft();
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

  // editorReady connects the editor; its current output becomes the unchanged baseline. With
  // a restored draft the editor shows the draft, and the stored text is the baseline, so the
  // draft is saved.
  const editorReady = useCallback(
    (get: () => string) => {
      getMarkdown.current = get;
      if (restored && baseline.current.markdown === null) {
        const draftText = get();
        // A draft whose text is the stored one changed only the title: the editor's own
        // output is the baseline, as without a draft.
        const stored = savedMarkdown ?? '';
        baseline.current = { title: baseline.current.title, markdown: restored.markdown === stored ? draftText : stored };
        if (isDirty()) {
          setSync('dirty');
          schedule();
        } else removeDraft(recordingId);
        return;
      }
      baseline.current = { title: baseline.current.title, markdown: get() };
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // discard stops all saving, e.g. before the summary is regenerated (after the user
  // confirmed that edits may be lost).
  const discard = useCallback(() => {
    discarded.current = true;
    clearTimers();
    window.clearTimeout(draftTimer.current);
    removeDraft(recordingId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // dirty says whether there are changes here that aren't saved yet; settled waits for a
  // save that is under way.
  const dirty = useCallback(() => isDirty(), []);
  const settled = useCallback(async () => {
    await inFlight.current;
  }, []);
  // known says whether the editor holds this revision of the text (it is ours).
  const known = useCallback((rev: number | undefined) => rev === undefined || rev === revision.current || rev === openedRevision.current, []);

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

  // Retry when the connection comes back or the user signed in again; ask before closing the
  // tab with unsaved changes (which are kept as a draft all the same); save what's left when
  // the note is left.
  useEffect(() => {
    const online = () => isDirty() && !conflicted.current && void save();
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (isDirty()) keepDraft();
      if (isDirty() || inFlight.current) e.preventDefault();
    };
    window.addEventListener('online', online);
    window.addEventListener(SIGNED_IN_EVENT, online);
    window.addEventListener('beforeunload', beforeUnload);
    return () => {
      window.removeEventListener('online', online);
      window.removeEventListener(SIGNED_IN_EVENT, online);
      window.removeEventListener('beforeunload', beforeUnload);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [save]);
  useEffect(
    () => () => {
      if (isDirty()) keepDraft();
      if (isDirty() && !conflicted.current) void save();
      clearTimers();
      window.clearTimeout(draftTimer.current);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // markdown is the text as it is now, unsaved changes included (null before the editor is ready).
  const markdown = useCallback(() => current().markdown, []);

  return { restored, title, setTitle, sync, error, changed, editorReady, save: () => save(), discard, dirty, settled, known, conflict, keepMine, markdown };
}
