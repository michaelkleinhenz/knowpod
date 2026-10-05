import { DragEvent, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { NoteTab, useNoteTabs } from '../context/NoteTabs';
import { useNotes } from '../context/NotesContext';
import { iconKind } from '../lib/recordings';
import { useContextMenu } from './ContextMenu';
import { NOTE_TYPE } from './FolderTree';
import { NoteIcon } from './Icons';

// TAB_TYPE marks a tab dragged along the bar to another place.
const TAB_TYPE = 'application/x-knowpod-tab';

// NoteTabBar shows the notes open in tabs above the main area (wide screens only). A tab is
// closed with its × or a middle click, and moved by dragging it; a note dragged from the list
// onto the bar opens in a new tab.
export function NoteTabBar() {
  const { t } = useTranslation();
  const tabs = useNoteTabs();
  const bar = useRef<HTMLDivElement>(null);
  const [dropOn, setDropOn] = useState(false);
  const activeId = tabs?.activeId;

  // The active tab is scrolled into view when there are more tabs than fit.
  useEffect(() => {
    if (!activeId) return;
    const el = bar.current?.querySelector<HTMLElement>(`[data-tab="${CSS.escape(activeId)}"]`);
    el?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }, [activeId, tabs?.tabs.length]);

  if (!tabs || tabs.tabs.length === 0) return null;

  const dropProps = {
    onDragOver: (e: DragEvent) => {
      if (!e.dataTransfer.types.includes(NOTE_TYPE)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
      setDropOn(true);
    },
    onDragLeave: (e: DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropOn(false);
    },
    onDrop: (e: DragEvent) => {
      setDropOn(false);
      const noteId = e.dataTransfer.getData(NOTE_TYPE);
      if (!noteId) return;
      e.preventDefault();
      tabs.open(noteId);
    },
  };

  return (
    <div ref={bar} className={`note-tabs${dropOn ? ' drop' : ''}`} role="navigation" aria-label={t('tabs.label')} {...dropProps}>
      <ul className="note-tabs-list">
        {tabs.tabs.map((tab) => (
          <TabItem key={tab.id} tab={tab} active={tab.id === activeId} />
        ))}
      </ul>
    </div>
  );
}

function TabItem({ tab, active }: { tab: NoteTab; active: boolean }) {
  const { t } = useTranslation();
  const tabs = useNoteTabs()!;
  const { recordings } = useNotes();
  const [dropBefore, setDropBefore] = useState(false);
  const rec = recordings?.find((r) => r.id === tab.id);
  const name = tab.title || t('conversations.untitled');
  const index = tabs.tabs.findIndex((x) => x.id === tab.id);
  const { handlers, menu } = useContextMenu([
    { label: t('tabs.close'), onSelect: () => tabs.close(tab.id) },
    ...(tabs.tabs.length > 1 ? [{ label: t('tabs.closeOthers'), onSelect: () => tabs.closeOthers(tab.id) }] : []),
    ...(index < tabs.tabs.length - 1 ? [{ label: t('tabs.closeRight'), onSelect: () => tabs.closeRight(tab.id) }] : []),
    { label: t('tabs.closeAll'), onSelect: () => tabs.closeAll() },
  ]);

  return (
    <li
      className={`note-tab${active ? ' active' : ''}${dropBefore ? ' drop-before' : ''}`}
      data-tab={tab.id}
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData(TAB_TYPE, tab.id);
        e.dataTransfer.effectAllowed = 'move';
      }}
      onDragOver={(e) => {
        if (!e.dataTransfer.types.includes(TAB_TYPE)) return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropBefore(true);
      }}
      onDragLeave={() => setDropBefore(false)}
      onDrop={(e) => {
        setDropBefore(false);
        const from = e.dataTransfer.getData(TAB_TYPE);
        if (!from) return;
        e.preventDefault();
        e.stopPropagation();
        tabs.move(from, tab.id);
      }}
      // A middle click closes the tab (without the browser's autoscroll).
      onMouseDown={(e) => e.button === 1 && e.preventDefault()}
      onAuxClick={(e) => {
        if (e.button !== 1) return;
        e.preventDefault();
        tabs.close(tab.id);
      }}
      {...handlers}
    >
      <Link to={`/conversations/${tab.id}`} className="note-tab-link" aria-current={active ? 'page' : undefined} title={name} draggable={false}>
        {rec && <NoteIcon type={iconKind(rec)} />}
        <span className="note-tab-title">{name}</span>
      </Link>
      <button type="button" className="note-tab-close" title={t('tabs.close')} aria-label={t('tabs.closeLabel', { title: name })} onClick={() => tabs.close(tab.id)}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" aria-hidden="true">
          <path d="M6 6l12 12M18 6L6 18" strokeLinecap="round" />
        </svg>
      </button>
      {menu}
    </li>
  );
}
