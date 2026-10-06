import { createContext, MouseEvent, ReactNode, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { useAuth } from '../auth';
import { title as titleOf } from '../lib/recordings';
import { useNotesIfAny } from './NotesContext';

// A note open in a tab of the main area. The title is remembered with it, so the tab is named
// before the notes list has loaded (or for a note the list doesn't hold).
export interface NoteTab {
  id: string;
  title: string;
}

interface NoteTabsState {
  tabs: NoteTab[];
  // activeId is the note shown now, or null while the main area shows another page.
  activeId: string | null;
  // open adds a tab for the note at the end and switches to it; a note already in a tab keeps
  // its place and is just switched to.
  open: (id: string) => void;
  // close removes the note's tab; closing the active tab shows its neighbour (or home).
  close: (id: string) => void;
  // closeOthers keeps only the note's tab; closeRight closes the tabs after it.
  closeOthers: (id: string) => void;
  closeRight: (id: string) => void;
  closeAll: () => void;
  // move puts the tab of the note at the place of the tab of another note (drag and drop).
  move: (id: string, before: string) => void;
}

// Tabs are only shown next to the sidebar; phones show one note at a time (see styles.css).
const WIDE = '(min-width: 901px)';

// tabsShown says whether the window is wide enough for the tab bar.
export function tabsShown(): boolean {
  return typeof window !== 'undefined' && window.matchMedia(WIDE).matches;
}

function storageKey(accountId: string): string {
  return `knowpod.noteTabs.${accountId}`;
}

function load(accountId: string): NoteTab[] {
  try {
    const list = JSON.parse(localStorage.getItem(storageKey(accountId)) ?? '[]') as unknown;
    if (!Array.isArray(list)) return [];
    return list.filter((t): t is NoteTab => !!t && typeof t.id === 'string' && typeof t.title === 'string');
  } catch {
    return [];
  }
}

const NoteTabsContext = createContext<NoteTabsState | null>(null);

// NoteTabsProvider keeps the notes open in tabs, remembered in the browser. The URL stays the
// source of truth for the shown note: opening a note that isn't in a tab yet shows it in the
// active tab (replacing the note there, like a browser tab following a link), a new note gets a
// tab of its own, and a note already in a tab switches to that tab.
export function NoteTabsProvider({ children }: { children: ReactNode }) {
  const { account } = useAuth();
  const accountId = account?.id ?? '';
  const notes = useNotesIfAny();
  const navigate = useNavigate();
  const { id, number } = useParams();
  const location = useLocation();
  const created = !!(location.state as { created?: boolean } | null)?.created;
  const [tabs, setTabsState] = useState<NoteTab[]>(() => load(accountId));
  // tabsRef has the tabs as last set, for the handlers called between renders.
  const tabsRef = useRef(tabs);
  // Signed in as someone else, their tabs are shown.
  const [owner, setOwner] = useState(accountId);
  if (owner !== accountId) {
    const list = load(accountId);
    setOwner(accountId);
    tabsRef.current = list;
    setTabsState(list);
  }
  const setTabs = useCallback(
    (next: NoteTab[]) => {
      tabsRef.current = next;
      setTabsState(next);
      try {
        localStorage.setItem(storageKey(accountId), JSON.stringify(next));
      } catch {
        // Private mode or storage full: the tabs are just not there next time.
      }
    },
    [accountId],
  );

  const recordings = notes?.recordings;
  const titleFor = useCallback((noteId: string, fallback = '') => {
    const rec = recordings?.find((r) => r.id === noteId);
    return rec ? titleOf(rec) : fallback;
  }, [recordings]);

  // shownId is the note shown in the active tab before the current page; following a link
  // from it replaces it. /n/12 is only on the way to a note, so it doesn't count.
  const shownId = useRef<string | null>(null);
  useEffect(() => {
    if (number) return;
    const prev = shownId.current;
    shownId.current = id ?? null;
    if (!id) return;
    const list = tabsRef.current;
    if (list.some((t) => t.id === id)) return;
    const tab = { id, title: titleFor(id) };
    const at = prev ? list.findIndex((t) => t.id === prev) : -1;
    if (at < 0) setTabs([...list, tab]);
    else if (created) setTabs([...list.slice(0, at + 1), tab, ...list.slice(at + 1)]);
    else setTabs([...list.slice(0, at), tab, ...list.slice(at + 1)]);
    // Only a change of the shown note opens or replaces a tab.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, number]);

  // The tabs take over changed titles from the list, and remember them.
  useEffect(() => {
    if (!recordings) return;
    const list = tabsRef.current;
    if (list.every((t) => titleFor(t.id, t.title) === t.title)) return;
    setTabs(list.map((t) => ({ ...t, title: titleFor(t.id, t.title) })));
  }, [recordings, titleFor, setTabs]);

  const activeId = id ?? null;

  const value = useMemo<NoteTabsState>(() => {
    // leave shows another tab when the shown note's tab is closed: the next one, else the
    // one before, else the home page (the list on phones).
    const leave = (closing: Set<string>, next: NoteTab[]) => {
      if (!activeId || !closing.has(activeId)) return;
      const list = tabsRef.current;
      const at = list.findIndex((t) => t.id === activeId);
      const after = list.slice(at + 1).find((t) => !closing.has(t.id));
      const before = list
        .slice(0, Math.max(0, at))
        .reverse()
        .find((t) => !closing.has(t.id));
      const to = after ?? before;
      // Set before navigating, so the shown tab isn't replaced by the one switched to.
      shownId.current = null;
      setTabs(next);
      // Phones show no tabs; there the list is shown again.
      navigate(to && tabsShown() ? `/conversations/${to.id}` : '/', { replace: true });
    };
    const closeSet = (closing: Set<string>) => {
      const next = tabsRef.current.filter((t) => !closing.has(t.id));
      if (next.length === tabsRef.current.length) return;
      if (activeId && closing.has(activeId)) leave(closing, next);
      else setTabs(next);
    };
    return {
      tabs,
      activeId,
      open: (noteId) => {
        const list = tabsRef.current;
        if (!list.some((t) => t.id === noteId)) setTabs([...list, { id: noteId, title: titleFor(noteId) }]);
        navigate(`/conversations/${noteId}`);
      },
      close: (noteId) => {
        // A note shown without a tab (e.g. right after it was opened) is left all the same.
        if (noteId === activeId && !tabsRef.current.some((t) => t.id === noteId)) {
          shownId.current = null;
          navigate('/', { replace: true });
          return;
        }
        closeSet(new Set([noteId]));
      },
      closeOthers: (noteId) => {
        const keep = tabsRef.current.find((t) => t.id === noteId);
        closeSet(new Set(tabsRef.current.filter((t) => t !== keep).map((t) => t.id)));
      },
      closeRight: (noteId) => {
        const at = tabsRef.current.findIndex((t) => t.id === noteId);
        if (at >= 0) closeSet(new Set(tabsRef.current.slice(at + 1).map((t) => t.id)));
      },
      closeAll: () => closeSet(new Set(tabsRef.current.map((t) => t.id))),
      move: (noteId, before) => {
        const list = tabsRef.current;
        const from = list.findIndex((t) => t.id === noteId);
        const to = list.findIndex((t) => t.id === before);
        if (from < 0 || to < 0 || from === to) return;
        const next = list.slice();
        const [tab] = next.splice(from, 1);
        next.splice(to, 0, tab);
        setTabs(next);
      },
    };
  }, [tabs, activeId, titleFor, setTabs, navigate]);

  return <NoteTabsContext.Provider value={value}>{children}</NoteTabsContext.Provider>;
}

// useNoteTabs returns the open tabs, or null outside the notes pages.
export function useNoteTabs(): NoteTabsState | null {
  return useContext(NoteTabsContext);
}

// useTabLink returns handlers for a link to a note: Ctrl/Cmd+click or a middle click opens the
// note in a new tab instead of showing it here (where tabs are shown at all).
export function useTabLink(): (noteId: string) => { onClick: (e: MouseEvent) => void; onAuxClick: (e: MouseEvent) => void; onMouseDown: (e: MouseEvent) => void } {
  const tabs = useNoteTabs();
  return (noteId: string) => ({
    onClick: (e: MouseEvent) => {
      if (!tabs || !(e.ctrlKey || e.metaKey) || e.button !== 0 || !tabsShown()) return;
      e.preventDefault();
      tabs.open(noteId);
    },
    onAuxClick: (e: MouseEvent) => {
      if (!tabs || e.button !== 1 || !tabsShown()) return;
      e.preventDefault();
      tabs.open(noteId);
    },
    // No autoscroll on a middle click.
    onMouseDown: (e: MouseEvent) => {
      if (tabs && e.button === 1 && tabsShown()) e.preventDefault();
    },
  });
}
