import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { DocIcon, RefreshIcon, SearchIcon } from '../components/Icons';
import { dayKey, dayLabel, formatTime, processing, statusLabel, title, when } from '../lib/recordings';

const POLL_MS = 10_000;

// Conversations lists all recordings, newest first, grouped by day. While any of them is
// still being processed, the list refreshes itself.
export function Conversations() {
  const [recordings, setRecordings] = useState<Recording[] | null>(null);
  const [aiReady, setAIReady] = useState(true);
  const [query, setQuery] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async () => {
    setRefreshing(true);
    try {
      const [list, ai] = await Promise.all([api.recordings(), api.openRouterSettings()]);
      setRecordings(list);
      setAIReady(ai.apiKeyConfigured && !!ai.transcriptionModel && !!ai.summaryModel);
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
    <section className="conversations">
      <div className="conversations-head">
        <h1>All Conversations</h1>
        <button type="button" className="pill-button" onClick={load} disabled={refreshing}>
          <RefreshIcon /> {refreshing ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>

      <label className="search">
        <SearchIcon />
        <input type="search" placeholder="Search" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search conversations" />
      </label>

      {!aiReady && recordings && recordings.length > 0 && (
        <p className="notice">
          Transcription and summaries are off until an OpenRouter API key and models are set in{' '}
          <Link to="/settings">Settings</Link>.
        </p>
      )}
      {error && <p className="error">{error}</p>}
      {!recordings && !error && <p className="muted">Loading…</p>}
      {recordings && recordings.length === 0 && (
        <p className="muted empty">
          No conversations yet. Recordings uploaded by your <Link to="/devices">devices</Link> or Pocket appear here.
        </p>
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
    </section>
  );
}
