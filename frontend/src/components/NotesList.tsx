import { ChangeEvent, DragEvent, useMemo, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { useAuth } from '../auth';
import { FolderTree, NOTE_TYPE } from './FolderTree';
import { FilterBar } from './SavedFilters';
import { TimerBar } from './TimeControls';
import { NewBoardIcon, NewFolderIcon, NewNoteIcon, RefreshIcon, SearchIcon, TrashIcon, UploadIcon } from './Icons';
import { NoteTreeRows, useNoteTree } from './NoteTree';
import { DueView } from './DueView';
import { TasksView } from './TasksView';
import { TrashView } from './TrashView';
import { errorText } from '../lib/errors';
import { parseFilter, searchMatcher } from '../lib/filterQuery';
import { dayKey, dayLabel, formatDate, formatTime, when } from '../lib/recordings';
import { lastFolder } from '../lib/lastFolder';

const ACCEPT = '.wav,.mp3,audio/wav,audio/x-wav,audio/wave,audio/mpeg';

// The list shows the notes by when they were created (grouped by day), the notes with a due
// date by that date, the notes in their folders, like files, or the open tasks by when they
// are due.
type View = 'timeline' | 'due' | 'folders' | 'tasks';
const VIEWS: View[] = ['timeline', 'due', 'folders', 'tasks'];
const VIEW_KEY = 'knowpod.notesView';
// FILTER_KEY remembers the saved filter the list is narrowed by.
const FILTER_KEY = 'knowpod.notesFilter';

function loadView(): View {
  try {
    const v = localStorage.getItem(VIEW_KEY);
    return VIEWS.includes(v as View) ? (v as View) : 'timeline';
  } catch {
    return 'timeline';
  }
}

function loadFilter(): string | null {
  try {
    return localStorage.getItem(FILTER_KEY);
  } catch {
    return null;
  }
}

interface UploadState {
  key: string;
  name: string;
  progress: number;
  error?: string;
  done?: boolean;
}

// NotesList lists the user's notes, either newest first grouped by day or in their folders,
// with an icon for each note's type. It creates text notes and accepts WAV/MP3 uploads
// (button or drag and drop). On desktop it is the sidebar next to the open note; on phones
// it is the start page.
export function NotesList({ activeId }: { activeId?: string }) {
  const { t } = useTranslation();
  const { account } = useAuth();
  const navigate = useNavigate();
  const { recordings, folders, filters, filterContext, trash, moveToTrash, aiReady, error, refreshing, reload: load, upsert } = useNotes();
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [uploads, setUploads] = useState<UploadState[]>([]);
  const [dragging, setDragging] = useState(false);
  const [view, setViewState] = useState(loadView);
  const [newFolder, setNewFolder] = useState(0);
  const [activeFilterId, setActiveFilterState] = useState(loadFilter);
  // showTrash shows only the trash instead of the notes.
  const [showTrash, setShowTrash] = useState(false);
  const [trashDrop, setTrashDrop] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const setView = (v: View) => {
    setViewState(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {
      // Not remembered; the list starts in the timeline next time.
    }
  };

  const setActiveFilter = (id: string | null) => {
    setActiveFilterState(id);
    try {
      if (id) localStorage.setItem(FILTER_KEY, id);
      else localStorage.removeItem(FILTER_KEY);
    } catch {
      // Not remembered.
    }
  };
  // A remembered filter that was deleted meanwhile no longer applies.
  const activeFilter = filters?.find((f) => f.id === activeFilterId) ?? null;

  async function upload(files: File[]) {
    for (const file of files) {
      const key = `${file.name}-${file.size}-${Date.now()}`;
      const update = (u: Partial<UploadState>) => setUploads((list) => list.map((x) => (x.key === key ? { ...x, ...u } : x)));
      setUploads((list) => [...list, { key, name: file.name, progress: 0 }]);
      try {
        await api.uploadRecording(file, (p) => update({ progress: p }));
        update({ progress: 1, done: true });
        load();
        setTimeout(() => setUploads((list) => list.filter((x) => x.key !== key)), 4000);
      } catch (err) {
        update({ error: errorText(err, t) });
      }
    }
  }

  // setDone checks a task off right away and stores it; a failure undoes the check mark.
  async function setDone(r: Recording, done: boolean) {
    setCreateError(null);
    upsert({ ...r, done });
    try {
      upsert(await api.setNoteDone(r.id, done));
    } catch (err) {
      upsert(r);
      setCreateError(errorText(err, t));
    }
  }

  // newNoteFolder is the folder new notes and boards go into: in the folder view the one last
  // opened (if it still exists), otherwise the top level.
  const newNoteFolder = (): string | undefined => {
    const id = view === 'folders' ? lastFolder() : '';
    return id && folders?.some((f) => f.id === id) ? id : undefined;
  };

  // createText makes an empty text note and opens it, ready to type its title.
  async function createText() {
    setCreating(true);
    setCreateError(null);
    try {
      const rec = await api.createTextNote(t('conversations.untitled'), '', undefined, undefined, newNoteFolder());
      upsert(rec);
      navigate(`/conversations/${rec.id}`, { state: { created: true } });
    } catch (err) {
      setCreateError(errorText(err, t));
    } finally {
      setCreating(false);
    }
  }

  // createSub makes an empty text note under parent and opens it, ready to type its title.
  async function createSub(parent: Recording) {
    setCreating(true);
    setCreateError(null);
    try {
      const rec = await api.createTextNote(t('conversations.untitled'), '', parent.id);
      upsert(rec);
      navigate(`/conversations/${rec.id}`, { state: { created: true } });
    } catch (err) {
      setCreateError(errorText(err, t));
    } finally {
      setCreating(false);
    }
  }

  // createBoard makes a board with the three default columns and opens it, ready to type
  // its title and choose the folder or label it shows.
  async function createBoard() {
    setCreating(true);
    setCreateError(null);
    try {
      const columns = (['todo', 'inProgress', 'done'] as const).map((k) => ({ id: '', name: t(`board.defaultColumns.${k}`) }));
      const rec = await api.createBoard(t('board.untitled'), { scope: { kind: '', id: '' }, columns }, newNoteFolder());
      upsert(rec);
      navigate(`/conversations/${rec.id}`, { state: { created: true } });
    } catch (err) {
      setCreateError(errorText(err, t));
    } finally {
      setCreating(false);
    }
  }

  function handleFiles(e: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(e.target.files ?? []);
    e.target.value = '';
    upload(files);
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    setDragging(false);
    upload(Array.from(e.dataTransfer.files));
  }

  // The search box takes the filter language (plain words search the titles); the chosen
  // saved filter narrows the list further.
  const search = useMemo(() => {
    const byQuery = query.trim() ? searchMatcher(query, filterContext) : null;
    const parsed = activeFilter ? parseFilter(activeFilter.query, filterContext) : null;
    const byFilter = parsed ? (parsed.ok ? parsed.match : () => false) : null;
    if (!byQuery && !byFilter) return null;
    return (r: Recording) => (!byQuery || byQuery(r)) && (!byFilter || byFilter(r));
  }, [query, activeFilter, filterContext]);
  const matches = useMemo(() => (search ? (recordings ?? []).filter(search) : (recordings ?? [])), [recordings, search]);

  // The timeline lists the notes by day, each sub-note under its parent note, whose time
  // orders them.
  const tree = useNoteTree(recordings ?? matches, matches);
  const groups = useMemo(() => {
    const list = tree.roots.filter((r) => tree.shown.get(r.id)).sort((a, b) => when(b).getTime() - when(a).getTime());
    const out: { key: string; day: Date; items: Recording[] }[] = [];
    for (const r of list) {
      const d = when(r);
      const key = dayKey(d);
      if (out.length === 0 || out[out.length - 1].key !== key) out.push({ key, day: d, items: [] });
      out[out.length - 1].items.push(r);
    }
    return out;
  }, [tree]);

  // Notes dragged onto the trash button (from the folder view) are moved to the trash.
  const trashDropProps = {
    onDragOver: (e: DragEvent) => {
      if (!e.dataTransfer.types.includes(NOTE_TYPE)) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = 'move';
      setTrashDrop(true);
    },
    onDragLeave: () => setTrashDrop(false),
    onDrop: (e: DragEvent) => {
      if (!e.dataTransfer.types.includes(NOTE_TYPE)) return;
      e.preventDefault();
      e.stopPropagation();
      setTrashDrop(false);
      const r = recordings?.find((n) => n.id === e.dataTransfer.getData(NOTE_TYPE));
      if (!r) return;
      setCreateError(null);
      moveToTrash(r).catch((err) => setCreateError(errorText(err, t)));
    },
  };

  return (
    <section
      className={`conversations notes-list${dragging ? ' dragging' : ''}`}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes('Files')) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragLeave={(e) => e.currentTarget === e.target && setDragging(false)}
      onDrop={handleDrop}
    >
      {/* The menu stays in place; only the items below it scroll (CSS). */}
      <div className="notes-list-top">
        <div className="conversations-head">
          <h1>{t('conversations.title')}</h1>
          <div className="head-actions">
            <button type="button" className="pill-button icon-only-mobile" onClick={createText} disabled={creating} aria-label={t('conversations.newNote')}>
              <NewNoteIcon /> <span>{t('conversations.newNote')}</span>
            </button>
            <button
              type="button"
              className="pill-button icon-only-mobile"
              onClick={createBoard}
              disabled={creating}
              title={t('conversations.newBoard')}
              aria-label={t('conversations.newBoard')}
            >
              <NewBoardIcon /> <span>{t('conversations.newBoard')}</span>
            </button>
            <button type="button" className="pill-button icon-only-mobile" onClick={() => fileInput.current?.click()} aria-label={t('conversations.uploadAudio')}>
              <UploadIcon /> <span>{t('conversations.upload')}</span>
            </button>
            <button type="button" className="pill-button icon-only-mobile" onClick={load} disabled={refreshing} aria-label={t('common.refresh')}>
              <RefreshIcon /> <span>{refreshing ? t('common.refreshing') : t('common.refresh')}</span>
            </button>
            <button
              type="button"
              className={`pill-button icon-only-mobile trash-toggle${showTrash ? ' active' : ''}${trashDrop ? ' drop' : ''}`}
              aria-pressed={showTrash}
              title={t(showTrash ? 'trash.hide' : 'trash.show')}
              aria-label={t(showTrash ? 'trash.hide' : 'trash.show')}
              onClick={() => setShowTrash((v) => !v)}
              {...trashDropProps}
            >
              <TrashIcon /> <span>{t('trash.title')}</span>
              {!!trash?.length && <span className="trash-count">{trash.length}</span>}
            </button>
          </div>
          <input ref={fileInput} type="file" accept={ACCEPT} multiple hidden onChange={handleFiles} />
      </div>

      <TimerBar />

      <label className="search">
        <SearchIcon />
        <input type="search" placeholder={t('common.search')} value={query} onChange={(e) => setQuery(e.target.value)} aria-label={t('conversations.searchLabel')} />
      </label>
      <FilterBar query={query} setQuery={setQuery} active={activeFilter?.id ?? null} setActive={setActiveFilter} />

      {!showTrash && (
        <div className="list-toolbar">
          <div className="segmented" role="tablist" aria-label={t('folders.viewLabel')}>
            {VIEWS.map((v) => (
              <button key={v} type="button" role="tab" aria-selected={view === v} className={view === v ? 'active' : ''} onClick={() => setView(v)}>
                {t(`folders.views.${v}`)}
              </button>
            ))}
          </div>
          {view === 'folders' && (
            <button type="button" className="pill-button" title={t('folders.new')} aria-label={t('folders.new')} onClick={() => setNewFolder((n) => n + 1)}>
              <NewFolderIcon /> <span>{t('folders.new')}</span>
            </button>
          )}
        </div>
      )}
      </div>

      <div className="notes-list-items">
        {showTrash && recordings && <TrashView search={search} activeId={activeId} aiReady={aiReady} onSetDone={(r, d) => void setDone(r, d)} />}

        {uploads.length > 0 && (
          <ul className="upload-list" aria-live="polite">
            {uploads.map((u) => (
              <li key={u.key} className={u.error ? 'failed' : ''}>
                <span className="upload-name">{u.name}</span>
                {u.error ? (
                  <span className="error">
                    {u.error}{' '}
                    <button type="button" className="link-button" onClick={() => setUploads((l) => l.filter((x) => x.key !== u.key))}>
                      {t('conversations.dismiss')}
                    </button>
                  </span>
                ) : (
                  <>
                    <progress max={1} value={u.progress} />
                    <span className="muted">{u.done ? t('conversations.uploaded') : `${Math.round(u.progress * 100)} %`}</span>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}

        {!aiReady && recordings && recordings.length > 0 && (
          <p className="notice">
            {account?.role === 'admin' ? <Trans i18nKey="conversations.aiOffAdmin" components={{ 1: <Link to="/admin?tab=general" /> }} /> : t('conversations.aiOff')}
          </p>
        )}
        {error && <p className="error">{error}</p>}
        {createError && <p className="error">{createError}</p>}
        {!recordings && !error && <p className="muted">{t('common.loading')}</p>}
        {!showTrash && recordings && recordings.length === 0 && (view === 'timeline' || view === 'folders') && (
          <div className="empty">
            <p className="muted">{t('conversations.empty')}</p>
            <p className="muted">
              <Trans i18nKey="conversations.emptyHint" components={{ 1: <Link to="/settings?tab=devices" />, 3: <Link to="/settings?tab=account" /> }} />
            </p>
            <div className="empty-actions">
              <button type="button" onClick={createText} disabled={creating}>
                {t('conversations.newNote')}
              </button>
              <button type="button" className="secondary-button" onClick={() => fileInput.current?.click()}>
                {t('conversations.uploadAudio')}
              </button>
            </div>
          </div>
        )}
        {!showTrash && view === 'due' && recordings && (recordings.length === 0 || matches.length > 0) && (
        <DueView notes={matches} activeId={activeId} aiReady={aiReady} onSetDone={(r, d) => void setDone(r, d)} onNewSub={creating ? undefined : (r) => void createSub(r)} />
      )}
      {!showTrash && view === 'tasks' && recordings && (
          <TasksView notes={matches} activeId={activeId} aiReady={aiReady} onSetDone={(r, d) => void setDone(r, d)} onNewSub={creating ? undefined : (r) => void createSub(r)} />
        )}

        {!showTrash && recordings && recordings.length > 0 && matches.length === 0 && view !== 'tasks' && (
          <p className="muted empty">{query.trim() ? t('conversations.noMatch', { query }) : t('filters.noMatch', { name: activeFilter?.name ?? '' })}</p>
        )}

        {!showTrash && view === 'folders' && recordings && (recordings.length > 0 || !!folders?.length || newFolder > 0) && (
          <FolderTree
            notes={matches}
            search={search}
            activeId={activeId}
            aiReady={aiReady}
            onSetDone={(r, d) => void setDone(r, d)}
            onNewSub={creating ? undefined : (r) => void createSub(r)}
            newFolder={newFolder}
          />
        )}

        {!showTrash &&
          view === 'timeline' &&
          groups.map((g) => {
            const { label, date } = dayLabel(g.day);
            return (
              <div key={g.key} className="day-group">
                <h2 className="day-heading">
                  {label} <span>{date}</span>
                </h2>
                <ul className="conversation-list">
                  <NoteTreeRows
                    list={g.items}
                    tree={tree}
                    searching={search !== null}
                    activeId={activeId}
                    aiReady={aiReady}
                    meta={(r, depth) => (depth === 0 ? formatTime(when(r)) : formatDate(when(r)))}
                    onSetDone={(r, d) => void setDone(r, d)}
                    onNewSub={creating ? undefined : (r) => void createSub(r)}
                  />
                </ul>
              </div>
            );
          })}
      </div>

      {dragging && <div className="drop-overlay">{t('conversations.dropHint')}</div>}
    </section>
  );
}
