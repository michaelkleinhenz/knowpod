import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { inline, Markdown } from '../components/Markdown';
import { formatBytes, formatDuration, processing, statusLabel, title, when } from '../lib/recordings';

type Tab = 'summary' | 'transcript' | 'source';

const TABS: { id: Tab; label: string }[] = [
  { id: 'summary', label: 'Summary' },
  { id: 'transcript', label: 'Transcript' },
  { id: 'source', label: 'Source' },
];

const POLL_MS = 5_000;

// Transcript shows the transcript line by line, with speaker labels ("Speaker 1:") set off.
function Transcript({ text }: { text: string }) {
  return (
    <div className="transcript">
      {text.split('\n').map((line, i) => {
        const m = /^([^:]{1,40}):\s(.*)$/.exec(line);
        if (!line.trim()) return <br key={i} />;
        return m ? (
          <p key={i}>
            <span className="speaker">{m[1]}</span> {m[2]}
          </p>
        ) : (
          <p key={i}>{line}</p>
        );
      })}
    </div>
  );
}

export function Conversation() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [rec, setRec] = useState<Recording | null>(null);
  const [aiReady, setAIReady] = useState(true);
  const [tab, setTab] = useState<Tab>('summary');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const [r, ai] = await Promise.all([api.recording(id), api.openRouterSettings()]);
      setRec(r);
      setAIReady(ai.apiKeyConfigured && !!ai.transcriptionModel && !!ai.summaryModel);
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const inProgress = rec ? processing(rec) : false;
  useEffect(() => {
    if (!inProgress) return;
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [inProgress, load]);

  async function act(action: () => Promise<unknown>, confirmText?: string) {
    if (confirmText && !window.confirm(confirmText)) return;
    setBusy(true);
    setError(null);
    try {
      await action();
      await load();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete() {
    if (!rec || !window.confirm(`Delete “${title(rec)}”? The audio, transcript and summary are removed permanently.`)) return;
    setBusy(true);
    try {
      await api.deleteRecording(rec.id);
      navigate('/', { replace: true });
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  }

  if (!rec) {
    return (
      <section className="conversation">
        <Link to="/" className="back-link">
          ← All conversations
        </Link>
        {error ? <p className="error">{error}</p> : <p className="muted">Loading…</p>}
      </section>
    );
  }

  const state = statusLabel(rec, aiReady);
  const d = when(rec);
  const pending = (what: string) =>
    rec.status === 'failed' ? (
      <div className="notice bad">
        <p>
          <strong>Processing failed.</strong> {rec.lastError}
        </p>
      </div>
    ) : (
      <p className="muted">{state ?? `No ${what} yet.`}</p>
    );

  return (
    <section className="conversation">
      <Link to="/" className="back-link">
        ← All conversations
      </Link>

      <div className="conversation-header">
        <div>
          <h1>{title(rec)}</h1>
          <p className="conversation-meta muted">
            {d.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })},{' '}
            {d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}
            {rec.format?.durationMs ? ` · ${formatDuration(rec.format.durationMs)}` : ''}
            {rec.source === 'pocket' ? ' · Pocket' : ''}
            {state && <span className={`state-pill${rec.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
          </p>
        </div>
        <div className="conversation-actions">
          <button
            type="button"
            className="small-button"
            disabled={busy || !rec.transcript}
            title={rec.transcript ? 'Summarize the transcript again' : 'Needs a transcript first'}
            onClick={() => act(() => api.resummarize(rec.id))}
          >
            Re-summarize
          </button>
          <button
            type="button"
            className="small-button"
            disabled={busy || !rec.audio}
            title={rec.audio ? 'Transcribe the audio again (also re-summarizes)' : 'The audio is not archived yet'}
            onClick={() => act(() => api.retranscribe(rec.id), 'Transcribe again? The current transcript and summary are replaced.')}
          >
            Re-transcribe
          </button>
          <button type="button" className="small-button danger" disabled={busy} onClick={handleDelete}>
            Delete
          </button>
        </div>
      </div>
      {error && <p className="error">{error}</p>}

      <div className="segmented" role="tablist" aria-label="View">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            className={tab === t.id ? 'active' : ''}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div className="card conversation-body" role="tabpanel">
        {tab === 'summary' &&
          (rec.summary?.markdown ? (
            <div className="prose">
              <Markdown text={rec.summary.markdown} />
              <p className="model-note">Summarized with {rec.summary.model || '—'}</p>
            </div>
          ) : (
            pending('summary')
          ))}

        {tab === 'transcript' &&
          (rec.transcript ? (
            <>
              {rec.transcript.text ? <Transcript text={rec.transcript.text} /> : <p className="muted">No speech was detected.</p>}
              <p className="model-note">Transcribed with {rec.transcript.model}</p>
            </>
          ) : (
            pending('transcript')
          ))}

        {tab === 'source' &&
          (rec.audio ? (
            <div className="source">
              <audio controls preload="metadata" src={api.audioURL(rec.id)}>
                Your browser can't play this audio.
              </audio>
              <dl className="facts">
                <dt>File</dt>
                <dd>
                  {rec.audio.contentType} · {formatBytes(rec.audio.size)}{' '}
                  <a className="small-button" href={api.audioURL(rec.id, true)} download>
                    Download
                  </a>
                </dd>
                {rec.format && (
                  <>
                    <dt>Audio</dt>
                    <dd>
                      {rec.format.sampleRate / 1000} kHz · {rec.format.channels === 1 ? 'mono' : `${rec.format.channels} channels`} ·{' '}
                      {rec.format.bitsPerSample} bit · {formatDuration(rec.format.durationMs)}
                    </dd>
                  </>
                )}
                <dt>Source</dt>
                <dd>{rec.source === 'pocket' ? inline(`Pocket recording \`${rec.recordingId}\``) : inline(`Device upload \`${rec.recordingId}\``)}</dd>
              </dl>
            </div>
          ) : (
            <p className="muted">{state ?? 'The audio is not available yet.'}</p>
          ))}
      </div>
    </section>
  );
}
