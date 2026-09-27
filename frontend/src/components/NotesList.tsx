import { ChangeEvent, DragEvent, useMemo, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { useAuth } from '../auth';
import { FolderTree } from './FolderTree';
import { NewBoardIcon, NewFolderIcon, NewNoteIcon, RefreshIcon, SearchIcon, UploadIcon } from './Icons';
import { NoteRow } from './NoteRow';
import { errorText } from '../lib/errors';
import { dayKey, dayLabel, formatTime, title, when } from '../lib/recordings';

const ACCEPT = '.wav,.mp3,audio/wav,audio/x-wav,audio/wave,audio/mpeg';

// The list shows the notes by time (grouped by day) or in their folders, like files.
type View = 'timeline' | 'folders';
const VIEWS: View[] = ['timeline', 'folders'];
const VIEW_KEY = 'knowpod.notesView';

function loadView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === 'folders' ? 'folders' : 'timeline';
  } catch {
    return 'timeline';
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
  const { recordings, folders, aiReady, error, refreshing, reload: load, upsert } = useNotes();
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [uploads, setUploads] = useState<UploadState[]>([]);
  const [dragging, setDragging] = useState(false);
  const [view, setViewState] = useState(loadView);
  const [newFolder, setNewFolder] = useState(0);
  const fileInput = useRef<HTMLInputElement>(null);

  const setView = (v: View) => {
    setViewState(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {
      // Not remembered; the list starts in the timeline next time.
    }
  };

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

  // createText makes an empty text note and opens it, ready to type its title.
  async function createText() {
    setCreating(true);
    setCreateError(null);
    try {
      const rec = await api.createTextNote(t('conversations.untitled'), '');
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
      const rec = await api.createBoard(t('board.untitled'), { scope: { kind: '', id: '' }, columns });
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

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    // "#12" (or "12") also finds note 12 by its number.
    const n = /^#?(\d+)$/.exec(q)?.[1];
    return (recordings ?? []).filter((r) => !q || title(r).toLowerCase().includes(q) || (!!n && String(r.number) === n));
  }, [recordings, query]);

  const groups = useMemo(() => {
    const list = matches.slice().sort((a, b) => when(b).getTime() - when(a).getTime());
    const out: { key: string; day: Date; items: Recording[] }[] = [];
    for (const r of list) {
      const d = when(r);
      const key = dayKey(d);
      if (out.length === 0 || out[out.length - 1].key !== key) out.push({ key, day: d, items: [] });
      out[out.length - 1].items.push(r);
    }
    return out;
  }, [matches]);

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
      <div className="conversations-head">
        <h1>{t('conversations.title')}</h1>
        <div className="head-actions">
          <button type="button" className="pill-button icon-only-mobile" onClick={createText} disabled={creating} aria-label={t('conversations.newNote')}>
            <NewNoteIcon /> <span>{t('conversations.newNote')}</span>
          </button>
          <button type="button" className="pill-button icon-only-mobile" onClick={createBoard} disabled={creating} title={t('conversations.newBoard')} aria-label={t('conversations.newBoard')}>
            <NewBoardIcon /> <span>{t('conversations.newBoard')}</span>
          </button>
          <button type="button" className="pill-button icon-only-mobile" onClick={() => fileInput.current?.click()} aria-label={t('conversations.uploadAudio')}>
            <UploadIcon /> <span>{t('conversations.upload')}</span>
          </button>
          <button type="button" className="pill-button icon-only-mobile" onClick={load} disabled={refreshing} aria-label={t('common.refresh')}>
            <RefreshIcon /> <span>{refreshing ? t('common.refreshing') : t('common.refresh')}</span>
          </button>
        </div>
        <input ref={fileInput} type="file" accept={ACCEPT} multiple hidden onChange={handleFiles} />
      </div>

      <label className="search">
        <SearchIcon />
        <input
          type="search"
          placeholder={t('common.search')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label={t('conversations.searchLabel')}
        />
      </label>

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
          {account?.role === 'admin' ? (
            <Trans i18nKey="conversations.aiOffAdmin" components={{ 1: <Link to="/settings" /> }} />
          ) : (
            t('conversations.aiOff')
          )}
        </p>
      )}
      {error && <p className="error">{error}</p>}
      {createError && <p className="error">{createError}</p>}
      {!recordings && !error && <p className="muted">{t('common.loading')}</p>}
      {recordings && recordings.length === 0 && (
        <div className="empty">
          <p className="muted">{t('conversations.empty')}</p>
          <p className="muted">
            <Trans i18nKey="conversations.emptyHint" components={{ 1: <Link to="/devices" />, 3: <Link to="/account" /> }} />
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
      {recordings && recordings.length > 0 && matches.length === 0 && (
        <p className="muted empty">{t('conversations.noMatch', { query })}</p>
      )}

      {view === 'folders' && recordings && (recordings.length > 0 || !!folders?.length || newFolder > 0) && (
        <FolderTree notes={matches} query={query} activeId={activeId} aiReady={aiReady} onSetDone={(r, d) => void setDone(r, d)} newFolder={newFolder} />
      )}

      {view === 'timeline' &&
        groups.map((g) => {
          const { label, date } = dayLabel(g.day);
          return (
            <div key={g.key} className="day-group">
              <h2 className="day-heading">
                {label} <span>{date}</span>
              </h2>
              <ul className="conversation-list">
                {g.items.map((r) => (
                  <NoteRow key={r.id} rec={r} active={r.id === activeId} aiReady={aiReady} meta={formatTime(when(r))} onSetDone={(r, d) => void setDone(r, d)} />
                ))}
              </ul>
            </div>
          );
        })}

      {dragging && <div className="drop-overlay">{t('conversations.dropHint')}</div>}
    </section>
  );
}
