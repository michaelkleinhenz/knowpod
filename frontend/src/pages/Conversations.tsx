import { ChangeEvent, DragEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { useAuth } from '../auth';
import { DocIcon, RefreshIcon, SearchIcon, UploadIcon } from '../components/Icons';
import { dayKey, dayLabel, formatTime, processing, statusLabel, title, when } from '../lib/recordings';

const POLL_MS = 10_000;
const ACCEPT = '.wav,.mp3,audio/wav,audio/x-wav,audio/wave,audio/mpeg';

interface UploadState {
  key: string;
  name: string;
  progress: number;
  error?: string;
  done?: boolean;
}

// Conversations lists the user's recordings, newest first, grouped by day, and accepts
// WAV/MP3 uploads (button or drag and drop). While anything is still being processed, the
// list refreshes itself.
export function Conversations() {
  const { account } = useAuth();
  const [recordings, setRecordings] = useState<Recording[] | null>(null);
  const [aiReady, setAIReady] = useState(true);
  const [query, setQuery] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [uploads, setUploads] = useState<UploadState[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    setRefreshing(true);
    try {
      const [list, ai] = await Promise.all([api.recordings(), api.aiStatus()]);
      setRecordings(list);
      setAIReady(ai.transcription && ai.summary);
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const busy = recordings?.some(processing) ?? false;
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [busy, load]);

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
        update({ error: (err as Error).message });
      }
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
      className={`conversations${dragging ? ' dragging' : ''}`}
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
        <h1>All Conversations</h1>
        <div className="head-actions">
          <button type="button" className="pill-button icon-only-mobile" onClick={() => fileInput.current?.click()} aria-label="Upload audio">
            <UploadIcon /> <span>Upload</span>
          </button>
          <button type="button" className="pill-button icon-only-mobile" onClick={load} disabled={refreshing} aria-label="Refresh">
            <RefreshIcon /> <span>{refreshing ? 'Refreshing…' : 'Refresh'}</span>
          </button>
        </div>
        <input ref={fileInput} type="file" accept={ACCEPT} multiple hidden onChange={handleFiles} />
      </div>

      <label className="search">
        <SearchIcon />
        <input type="search" placeholder="Search" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search conversations" />
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
                    Dismiss
                  </button>
                </span>
              ) : (
                <>
                  <progress max={1} value={u.progress} />
                  <span className="muted">{u.done ? 'Uploaded' : `${Math.round(u.progress * 100)}%`}</span>
                </>
              )}
            </li>
          ))}
        </ul>
      )}

      {!aiReady && recordings && recordings.length > 0 && (
        <p className="notice">
          Transcription and summaries are off until an administrator sets up OpenRouter
          {account?.role === 'admin' ? (
            <>
              {' '}
              in <Link to="/settings">Settings</Link>
            </>
          ) : null}
          .
        </p>
      )}
      {error && <p className="error">{error}</p>}
      {!recordings && !error && <p className="muted">Loading…</p>}
      {recordings && recordings.length === 0 && (
        <div className="empty">
          <p className="muted">No conversations yet.</p>
          <p className="muted">
            Upload a WAV or MP3 file, record with one of your <Link to="/devices">devices</Link>, or connect Pocket on the{' '}
            <Link to="/account">Account</Link> page.
          </p>
          <button type="button" onClick={() => fileInput.current?.click()}>
            Upload audio
          </button>
        </div>
      )}
      {recordings && recordings.length > 0 && groups.length === 0 && <p className="muted empty">No conversation matches “{query}”.</p>}

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
                    <Link to={`/conversations/${r.id}`} className="conversation-item">
                      <DocIcon />
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

      {dragging && <div className="drop-overlay">Drop WAV or MP3 files to upload</div>}
    </section>
  );
}
