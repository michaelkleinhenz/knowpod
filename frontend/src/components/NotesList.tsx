import { ChangeEvent, DragEvent, useMemo, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { useNotes } from '../context/NotesContext';
import { useAuth } from '../auth';
import { NewNoteIcon, NoteIcon, RefreshIcon, SearchIcon, UploadIcon } from './Icons';
import { errorText } from '../lib/errors';
import { dayKey, dayLabel, formatTime, noteType, statusLabel, title, when } from '../lib/recordings';

const ACCEPT = '.wav,.mp3,audio/wav,audio/x-wav,audio/wave,audio/mpeg';

interface UploadState {
  key: string;
  name: string;
  progress: number;
  error?: string;
  done?: boolean;
}

// NotesList lists the user's notes, newest first, grouped by day, with an icon for each
// note's type. It creates text notes and accepts WAV/MP3 uploads (button or drag and drop). On desktop it is the sidebar next to the open note;
// on phones it is the start page.
export function NotesList({ activeId }: { activeId?: string }) {
  const { t } = useTranslation();
  const { account } = useAuth();
  const navigate = useNavigate();
  const { recordings, aiReady, error, refreshing, reload: load, upsert } = useNotes();
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [uploads, setUploads] = useState<UploadState[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

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

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (recordings ?? [])
      .filter((r) => !q || title(r).toLowerCase().includes(q))
      .sort((a, b) => when(b).getTime() - when(a).getTime());
    const out: { key: string; day: Date; items: Recording[] }[] = [];
    for (const r of list) {
      const d = when(r);
      const key = dayKey(d);
      if (out.length === 0 || out[out.length - 1].key !== key) out.push({ key, day: d, items: [] });
      out[out.length - 1].items.push(r);
    }
    return out;
  }, [recordings, query]);

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
      {recordings && recordings.length > 0 && groups.length === 0 && (
        <p className="muted empty">{t('conversations.noMatch', { query })}</p>
      )}

      {groups.map((g) => {
        const { label, date } = dayLabel(g.day);
        return (
          <div key={g.key} className="day-group">
            <h2 className="day-heading">
              {label} <span>{date}</span>
            </h2>
            <ul className="conversation-list">
              {g.items.map((r) => {
                const state = statusLabel(r, aiReady);
                return (
                  <li key={r.id}>
                    <Link
                      to={`/conversations/${r.id}`}
                      className={`conversation-item${r.id === activeId ? ' active' : ''}`}
                      aria-current={r.id === activeId ? 'page' : undefined}
                    >
                      <NoteIcon type={noteType(r)} label={t(`conversations.types.${noteType(r)}`)} />
                      <span className="conversation-title">
                        {title(r)}
                        {state && <span className={`state-pill${r.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
                      </span>
                      <span className="conversation-time">{formatTime(when(r))}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}

      {dragging && <div className="drop-overlay">{t('conversations.dropHint')}</div>}
    </section>
  );
}
