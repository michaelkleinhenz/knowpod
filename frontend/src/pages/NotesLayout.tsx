import { useTranslation } from 'react-i18next';
import { useRef, useState } from 'react';
import type { CSSProperties, KeyboardEvent, PointerEvent } from 'react';
import { Outlet, useLocation, useParams } from 'react-router-dom';
import { NotesList } from '../components/NotesList';
import { Briefing } from './Briefing';
import { NotesProvider } from '../context/NotesContext';
import { DEFAULT_SIDEBAR_WIDTH, clampSidebarWidth, useSidebarWidth } from '../lib/sidebarWidth';

// NotesLayout shows the notes list as a sidebar next to the open note (or the time log) on
// desktop. On narrow screens only one of them is visible: the list at "/", the note when one
// is open, the briefing at "/briefing" (CSS).
// On desktop the sidebar's right edge can be dragged to make it wider or narrower.
export function NotesLayout() {
  const { t } = useTranslation();
  const { id, number } = useParams();
  const { pathname } = useLocation();
  const hasMain = !!(id || number) || ['/time', '/ask', '/share', '/briefing'].includes(pathname);
  const [width, setWidth] = useSidebarWidth();
  const [resizing, setResizing] = useState(false);
  const drag = useRef<{ x: number; width: number } | null>(null);

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
      <div
        className={`notes-layout${hasMain ? ' has-note' : ''}${resizing ? ' resizing' : ''}`}
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
        <div className="notes-main">
          <Outlet />
        </div>
      </div>
    </NotesProvider>
  );
}

// NotesHome fills the main area on desktop while no note is open: today's briefing, the
// start page. (On phones "/" shows the list; the briefing is at "/briefing".)
export function NotesHome() {
  return <Briefing />;
}
