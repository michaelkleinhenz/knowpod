import { useTranslation } from 'react-i18next';
import { useEffect, useRef, useState } from 'react';
import type { CSSProperties, KeyboardEvent, PointerEvent } from 'react';
import { Outlet, useLocation, useParams } from 'react-router-dom';
import { NotesList } from '../components/NotesList';
import { Briefing } from './Briefing';
import { NotesProvider } from '../context/NotesContext';
import { NoteTabsProvider } from '../context/NoteTabs';
import { NoteTabBar } from '../components/NoteTabs';
import { SidebarIcon } from '../components/Icons';
import { DEFAULT_SIDEBAR_WIDTH, clampSidebarWidth, useSidebarCollapsed, useSidebarWidth } from '../lib/sidebarWidth';

// NotesLayout shows the notes list as a sidebar next to the open note (or the time log) on
// desktop. On narrow screens only one of them is visible: the list at "/", the note when one
// is open, the briefing at "/briefing" (CSS).
// On desktop the sidebar's right edge can be dragged to make it wider or narrower, and the
// sidebar can be collapsed (button or Ctrl/Cmd+\) to give the open note the whole width.
// Notes open in tabs above the main area there, so several can be kept open at once.
export function NotesLayout() {
  const { t } = useTranslation();
  const { id, number } = useParams();
  const { pathname } = useLocation();
  const hasMain = !!(id || number) || ['/time', '/done', '/ask', '/share', '/briefing'].includes(pathname);
  const [width, setWidth] = useSidebarWidth();
  const [resizing, setResizing] = useState(false);
  const drag = useRef<{ x: number; width: number } | null>(null);
  const [collapsed, setCollapsed] = useSidebarCollapsed();

  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key === '\\') {
        e.preventDefault();
        setCollapsed(!collapsed);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [collapsed, setCollapsed]);

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, width };
    setResizing(true);
  };
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (drag.current) setWidth(drag.current.width + e.clientX - drag.current.x);
  };
  const onPointerUp = () => {
    drag.current = null;
    setResizing(false);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.shiftKey ? 64 : 16;
    if (e.key === 'ArrowLeft') setWidth(width - step);
    else if (e.key === 'ArrowRight') setWidth(width + step);
    else if (e.key === 'Home') setWidth(DEFAULT_SIDEBAR_WIDTH);
    else return;
    e.preventDefault();
  };

  // A width remembered on a wider window is narrowed to fit this one.
  const shown = typeof window === 'undefined' ? width : clampSidebarWidth(width);
  return (
    <NotesProvider>
      <NoteTabsProvider>
        <div
          className={`notes-layout${hasMain ? ' has-note' : ''}${resizing ? ' resizing' : ''}${collapsed ? ' sidebar-collapsed' : ''}`}
          style={{ '--sidebar-width': `${shown}px` } as CSSProperties}
        >
          <aside className="notes-sidebar">
            <NotesList activeId={id} />
          </aside>
          <div
            className="sidebar-resizer"
            role="separator"
            aria-orientation="vertical"
            aria-label={t('conversations.resizeSidebar')}
            title={t('conversations.resizeSidebar')}
            aria-valuenow={shown}
            tabIndex={0}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerCancel={onPointerUp}
            onDoubleClick={() => setWidth(DEFAULT_SIDEBAR_WIDTH)}
            onKeyDown={onKeyDown}
          />
          <button
            type="button"
            className="sidebar-toggle"
            aria-expanded={!collapsed}
            aria-label={t(collapsed ? 'conversations.expandSidebar' : 'conversations.collapseSidebar')}
            title={t(collapsed ? 'conversations.expandSidebar' : 'conversations.collapseSidebar')}
            onClick={() => setCollapsed(!collapsed)}
          >
            <SidebarIcon collapsed={collapsed} />
          </button>
          <div className="notes-main">
            <NoteTabBar />
            <Outlet />
          </div>
        </div>
      </NoteTabsProvider>
    </NotesProvider>
  );
}

// NotesHome fills the main area on desktop while no note is open: today's briefing, the
// start page. (On phones "/" shows the list; the briefing is at "/briefing".)
export function NotesHome() {
  return <Briefing />;
}
