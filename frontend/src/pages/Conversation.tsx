import { lazy, ReactNode, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { CopyButton } from '../components/CopyButton';
import { inline, Markdown } from '../components/Markdown';
import { SummaryDetails } from '../components/SummaryDetails';
import { useNotes } from '../context/NotesContext';
import { Sync, useAutosave } from '../hooks/useAutosave';
import { locale } from '../i18n';
import { errorText } from '../lib/errors';
import { formatBytes, formatClock, formatDate, formatDuration, processing, statusLabel, title as titleOf, when } from '../lib/recordings';

// The rich text editor is downloaded on first use; the summary is shown read-only meanwhile.
const SummaryEditor = lazy(() => import('../components/SummaryEditor'));

type Tab = 'summary' | 'transcript' | 'source';
const TABS: Tab[] = ['summary', 'transcript', 'source'];
const POLL_MS = 5_000;

const TIME = /^\[(?:(\d{1,2}):)?(\d{1,3}):(\d{2})\]\s*/;

// Transcript shows the transcript line by line. Time stamps ("[1:05]") become buttons that
// play the audio from there; speaker labels ("Speaker 1:") are set off.
function Transcript({ text, onSeek }: { text: string; onSeek: (ms: number) => void }) {
  const { t } = useTranslation();
  return (
    <div className="transcript">
      {text.split('\n').map((raw, i) => {
        if (!raw.trim()) return <br key={i} />;
        let line = raw;
        let time: ReactNode = null;
        const ts = TIME.exec(line);
        if (ts) {
          const ms = ((Number(ts[1] ?? 0) * 60 + Number(ts[2])) * 60 + Number(ts[3])) * 1000;
          line = line.slice(ts[0].length);
          time = (
            <button type="button" className="time-link" title={t('conversation.playFrom', { time: formatClock(ms) })} onClick={() => onSeek(ms)}>
              {formatClock(ms)}
            </button>
          );
        }
        const m = /^([^:]{1,40}):\s(.*)$/.exec(line);
        return (
          <p key={i}>
            {time}
            {m ? (
              <>
                <span className="speaker">{m[1]}</span> {m[2]}
              </>
            ) : (
              line
            )}
          </p>
        );
      })}
    </div>
  );
}

// Highlights shows the recording's highlights as markers on a timeline and as a list; both
// play the audio from the marked moment.
function Highlights({ highlights, durationMs, onSeek }: { highlights: { offsetMs: number }[]; durationMs: number; onSeek: (ms: number) => void }) {
  const { t } = useTranslation();
  return (
    <div className="highlights">
      <h3>{t('conversation.highlights')}</h3>
      <p className="muted field-note">{t('conversation.highlightsHint')}</p>
      {durationMs > 0 && (
        <div className="timeline" role="group" aria-label={t('conversation.timeline')}>
          {highlights.map((h) => (
            <button
              key={h.offsetMs}
              type="button"
              className="timeline-marker"
              style={{ left: `${Math.min(100, (h.offsetMs / durationMs) * 100)}%` }}
              title={t('conversation.highlightAt', { time: formatClock(h.offsetMs) })}
              aria-label={t('conversation.highlightAt', { time: formatClock(h.offsetMs) })}
              onClick={() => onSeek(h.offsetMs)}
            />
          ))}
        </div>
      )}
      <ul className="highlight-list">
        {highlights.map((h, i) => (
          <li key={h.offsetMs}>
            <button type="button" className="time-link" onClick={() => onSeek(h.offsetMs)}>
              {formatClock(h.offsetMs)}
            </button>
            <span className="muted">{t('conversation.highlightN', { n: i + 1 })}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function SyncState({ sync, error, onRetry }: { sync: Sync; error: string | null; onRetry: () => void }) {
  const { t } = useTranslation();
  const text =
    sync === 'saved'
      ? t('editor.sync.saved')
      : sync === 'dirty'
        ? t('editor.sync.dirty')
        : sync === 'saving'
          ? t('editor.sync.saving')
          : sync === 'offline'
            ? t('editor.sync.offline')
            : t('editor.sync.error', { error: error ?? '' });
  return (
    <span className={`sync-state ${sync}`} role="status" aria-live="polite">
      <span className="sync-dot" aria-hidden="true" />
      <span>{text}</span>
      {(sync === 'error' || sync === 'offline') && (
        <button type="button" className="link-button" onClick={onRetry}>
          {t('editor.sync.retry')}
        </button>
      )}
    </span>
  );
}

interface BodyProps {
  rec: Recording;
  aiReady: boolean;
  tab: Tab;
  setTab: (t: Tab) => void;
  setRec: (r: Recording) => void;
  reload: () => Promise<void>;
}

// NoteBody is a note's page below the back link. It is re-created when a new summary
// arrives (see the key in Conversation), so the editor always starts from the stored text.
function NoteBody({ rec, aiReady, tab, setTab, setRec, reload }: BodyProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const notes = useNotes();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const summary = rec.summary;
  const editable = !!summary;
  const audio = useRef<HTMLAudioElement>(null);
  const [seek, setSeek] = useState<number | null>(null);
  const [durationMs, setDurationMs] = useState(rec.format?.durationMs ?? 0);
  const highlights = rec.highlights ?? [];

  // Play from a moment: switch to the audio and start there once it is on the page.
  const seekTo = (ms: number) => {
    setTab('source');
    setSeek(ms);
  };
  useEffect(() => {
    const el = audio.current;
    if (seek === null || tab !== 'source' || !el) return;
    el.currentTime = seek / 1000;
    void el.play().catch(() => undefined); // autoplay may be refused; the position is set anyway
    setSeek(null);
  }, [seek, tab]);
  const autosave = useAutosave(rec.id, summary?.title ?? titleOf(rec), setRec);

  async function act(action: () => Promise<unknown>, confirmText?: string) {
    if (confirmText && !window.confirm(confirmText)) return;
    setBusy(true);
    setError(null);
    try {
      await action();
      await reload();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  // Regenerating replaces the summary, so pending edits are dropped (after the user
  // confirmed) instead of being saved over the new summary later.
  const regenerate = (fn: () => Promise<unknown>) => {
    const confirmText = summary?.editedAt || autosave.sync !== 'saved' ? t('editor.regenerateEditedConfirm') : t('details.regenerateConfirm');
    return act(async () => {
      autosave.discard();
      await fn();
    }, confirmText);
  };

  async function handleDelete() {
    if (!window.confirm(t('conversation.deleteConfirm', { title: autosave.title || titleOf(rec) }))) return;
    setBusy(true);
    try {
      autosave.discard();
      await api.deleteRecording(rec.id);
      notes.remove(rec.id);
      navigate('/', { replace: true });
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  const state = statusLabel(rec, aiReady);
  const d = when(rec);
  const pending = (empty: string) =>
    rec.status === 'failed' ? (
      <div className="notice bad">
        <p>
          <strong>{t('conversation.failed')}</strong> {rec.lastError}
        </p>
      </div>
    ) : (
      <p className="muted">{state ?? empty}</p>
    );
  const sourceBadge = rec.source === 'pocket' ? t('conversation.sourcePocket') : rec.source === 'upload' ? t('conversation.sourceUpload') : '';

  return (
    <>
      <div className="conversation-header">
        <div className="title-block">
          {editable ? (
            <input
              className="title-input"
              aria-label={t('editor.title')}
              maxLength={200}
              value={autosave.title}
              onChange={(e) => autosave.setTitle(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
              }}
            />
          ) : (
            <h1>{titleOf(rec)}</h1>
          )}
          <p className="conversation-meta muted">
            {d.toLocaleDateString(locale(), { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })},{' '}
            {d.toLocaleTimeString(locale(), { hour: 'numeric', minute: '2-digit' })}
            {rec.format?.durationMs ? ` · ${formatDuration(rec.format.durationMs)}` : ''}
            {sourceBadge && ` · ${sourceBadge}`}
            {state && <span className={`state-pill${rec.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
          </p>
          {editable && <SyncState sync={autosave.sync} error={autosave.error} onRetry={() => void autosave.save()} />}
        </div>
        <div className="conversation-actions">
          <button
            type="button"
            className="small-button"
            disabled={busy || !rec.transcript}
            title={rec.transcript ? undefined : t('conversation.needsTranscript')}
            onClick={() => regenerate(() => api.resummarize(rec.id))}
          >
            {t('conversation.resummarize')}
          </button>
          <button
            type="button"
            className="small-button"
            disabled={busy || !rec.audio}
            title={rec.audio ? t('conversation.retranscribeTitle') : t('conversation.notArchived')}
            onClick={() =>
              act(async () => {
                autosave.discard();
                await api.retranscribe(rec.id);
              }, t('conversation.retranscribeConfirm'))
            }
          >
            {t('conversation.retranscribe')}
          </button>
          <button type="button" className="small-button danger" disabled={busy} onClick={handleDelete}>
            {t('common.delete')}
          </button>
        </div>
      </div>
      {error && <p className="error">{error}</p>}

      <div className="segmented" role="tablist" aria-label={t('conversation.viewLabel')}>
        {TABS.map((id) => (
          <button key={id} type="button" role="tab" aria-selected={tab === id} className={tab === id ? 'active' : ''} onClick={() => setTab(id)}>
            {t(`conversation.tabs.${id}`)}
          </button>
        ))}
      </div>

      <div className="conversation-body" role="tabpanel">
        {/* The summary stays mounted on other tabs so unsaved edits and the undo history survive. */}
        <div hidden={tab !== 'summary'}>
          <div className="summary-head">
            <div className="summary-tools">
              {rec.transcript && <SummaryDetails rec={rec} onRegenerate={(fn) => regenerate(fn)} />}
              {summary?.markdown && (
                <a className="ghost-button" href={api.downloadURL(rec.id, 'summary')} download title={t('conversation.downloadSummary')}>
                  {t('conversation.download')}
                </a>
              )}
              {summary?.markdown && (
                <CopyButton
                  className="ghost-button"
                  text={`# ${autosave.title}\n\n${summary.markdown}`}
                  label={t('conversation.copySummary')}
                />
              )}
            </div>
          </div>
          {summary ? (
            <>
              <Suspense
                fallback={
                  <div className="prose editor-content">
                    <Markdown text={summary.markdown ?? ''} />
                  </div>
                }
              >
                <SummaryEditor
                  markdown={summary.markdown ?? ''}
                  onReady={autosave.editorReady}
                  onChange={autosave.changed}
                  onSaveShortcut={() => void autosave.save()}
                />
              </Suspense>
              <p className="model-note">
                {summary.model && t('conversation.summarizedWith', { model: summary.model })}
                {summary.editedAt && (
                  <>
                    {summary.model && ' · '}
                    {t('editor.edited', { date: formatDate(summary.editedAt, { dateStyle: 'medium', timeStyle: 'short' }) })}
                  </>
                )}
              </p>
            </>
          ) : (
            pending(t('conversation.noSummary'))
          )}
        </div>

        {tab === 'transcript' &&
          (rec.transcript ? (
            <>
              {rec.transcript.text ? (
                <Transcript text={rec.transcript.text} onSeek={seekTo} />
              ) : (
                <p className="muted">{t('conversation.noSpeech')}</p>
              )}
              <p className="model-note">
                {t('conversation.transcribedWith', { model: rec.transcript.model })} ·{' '}
                <a href={api.downloadURL(rec.id, 'transcript')} download title={t('conversation.downloadTranscript')}>
                  {t('conversation.download')}
                </a>
              </p>
            </>
          ) : (
            pending(t('conversation.noTranscript'))
          ))}

        {tab === 'source' &&
          (rec.audio ? (
            <div className="source">
              <audio
                ref={audio}
                controls
                preload="metadata"
                src={api.audioURL(rec.id)}
                onLoadedMetadata={(e) => {
                  const d = e.currentTarget.duration;
                  if (!durationMs && Number.isFinite(d)) setDurationMs(d * 1000);
                  if (seek !== null) {
                    e.currentTarget.currentTime = seek / 1000;
                    setSeek(null);
                  }
                }}
              >
                {t('conversation.noAudioSupport')}
              </audio>
              {highlights.length > 0 && <Highlights highlights={highlights} durationMs={durationMs} onSeek={seekTo} />}
              <dl className="facts">
                <dt>{t('conversation.file')}</dt>
                <dd>
                  {rec.audio.contentType} · {formatBytes(rec.audio.size)}{' '}
                  <a className="small-button" href={api.audioURL(rec.id, true)} download>
                    {t('conversation.download')}
                  </a>
                </dd>
                {rec.format && (
                  <>
                    <dt>{t('conversation.audio')}</dt>
                    <dd>
                      {new Intl.NumberFormat(locale()).format(rec.format.sampleRate / 1000)} kHz ·{' '}
                      {rec.format.channels === 1 ? t('conversation.mono') : t('conversation.channels', { count: rec.format.channels })} ·{' '}
                      {rec.format.bitsPerSample} bit · {formatDuration(rec.format.durationMs)}
                    </dd>
                  </>
                )}
                <dt>{t('conversation.source')}</dt>
                <dd>
                  {rec.source === 'pocket'
                    ? inline(`${t('conversation.pocketRecording')} \`${rec.recordingId}\``)
                    : rec.source === 'upload'
                      ? t('conversation.browserUpload')
                      : inline(`${t('conversation.deviceUpload')} \`${rec.recordingId}\``)}
                </dd>
              </dl>
            </div>
          ) : (
            <p className="muted">{state ?? t('conversation.audioUnavailable')}</p>
          ))}
      </div>
    </>
  );
}

export function Conversation() {
  const { t } = useTranslation();
  const { id = '' } = useParams();
  const notes = useNotes();
  const [rec, setRecState] = useState<Recording | null>(null);
  // Changes to the open note (saves, processing progress) also update the sidebar.
  const { upsert } = notes;
  const setRec = useCallback(
    (r: Recording) => {
      setRecState(r);
      upsert(r);
    },
    [upsert],
  );
  const [aiReady, setAIReady] = useState(true);
  const [tab, setTab] = useState<Tab>('summary');
  const [error, setError] = useState<string | null>(null);

  // The note that is open now; answers for a note opened earlier are ignored.
  const openId = useRef(id);
  openId.current = id;

  const load = useCallback(async () => {
    try {
      const [r, ai] = await Promise.all([api.recording(id), api.aiStatus()]);
      if (openId.current !== id) return;
      setRec(r);
      setAIReady(ai.transcription && ai.summary);
      setError(null);
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [id, t, setRec]);

  // Opening another note starts on its summary.
  useEffect(() => {
    setRecState(null);
    setTab('summary');
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const inProgress = rec ? processing(rec) : false;
  useEffect(() => {
    if (!inProgress) return;
    const timer = setInterval(load, POLL_MS);
    return () => clearInterval(timer);
  }, [inProgress, load]);

  return (
    <section className="conversation">
      <Link to="/" className="back-link">
        {t('conversation.back')}
      </Link>
      {rec ? (
        <NoteBody
          key={`${rec.id}:${rec.summary?.createdAt ?? ''}`}
          rec={rec}
          aiReady={aiReady}
          tab={tab}
          setTab={setTab}
          setRec={setRec}
          reload={load}
        />
      ) : error ? (
        <p className="error">{error}</p>
      ) : (
        <p className="muted">{t('common.loading')}</p>
      )}
    </section>
  );
}
