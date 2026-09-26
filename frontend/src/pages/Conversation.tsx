import { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { CopyButton } from '../components/CopyButton';
import { inline, Markdown } from '../components/Markdown';
import { SummaryDetails } from '../components/SummaryDetails';
import { locale } from '../i18n';
import { errorText } from '../lib/errors';
import { formatBytes, formatDate, formatDuration, processing, statusLabel, title, when } from '../lib/recordings';

// The rich text editor is only downloaded when a summary is edited.
const SummaryEditor = lazy(() => import('../components/SummaryEditor'));

type Tab = 'summary' | 'transcript' | 'source';
const TABS: Tab[] = ['summary', 'transcript', 'source'];
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
  const { t } = useTranslation();
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [rec, setRec] = useState<Recording | null>(null);
  const [aiReady, setAIReady] = useState(true);
  const [tab, setTab] = useState<Tab>('summary');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);

  const load = useCallback(async () => {
    try {
      const [r, ai] = await Promise.all([api.recording(id), api.aiStatus()]);
      setRec(r);
      setAIReady(ai.transcription && ai.summary);
      setError(null);
    } catch (err) {
      setError(errorText(err, t));
    }
  }, [id, t]);

  useEffect(() => {
    load();
  }, [load]);

  const inProgress = rec ? processing(rec) : false;
  useEffect(() => {
    if (!inProgress) return;
    const timer = setInterval(load, POLL_MS);
    return () => clearInterval(timer);
  }, [inProgress, load]);

  async function act(action: () => Promise<unknown>, confirmText?: string) {
    if (confirmText && !window.confirm(confirmText)) return;
    setBusy(true);
    setError(null);
    try {
      await action();
      await load();
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete() {
    if (!rec || !window.confirm(t('conversation.deleteConfirm', { title: title(rec) }))) return;
    setBusy(true);
    try {
      await api.deleteRecording(rec.id);
      navigate('/', { replace: true });
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  if (!rec) {
    return (
      <section className="conversation">
        <Link to="/" className="back-link">
          {t('conversation.back')}
        </Link>
        {error ? <p className="error">{error}</p> : <p className="muted">{t('common.loading')}</p>}
      </section>
    );
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
    <section className="conversation">
      <Link to="/" className="back-link">
        {t('conversation.back')}
      </Link>

      <div className="conversation-header">
        <div>
          <h1>{title(rec)}</h1>
          <p className="conversation-meta muted">
            {d.toLocaleDateString(locale(), { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })},{' '}
            {d.toLocaleTimeString(locale(), { hour: 'numeric', minute: '2-digit' })}
            {rec.format?.durationMs ? ` · ${formatDuration(rec.format.durationMs)}` : ''}
            {sourceBadge && ` · ${sourceBadge}`}
            {state && <span className={`state-pill${rec.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
          </p>
        </div>
        <div className="conversation-actions">
          <button
            type="button"
            className="small-button"
            disabled={busy || editing || !rec.transcript}
            title={editing ? t('conversation.finishEditing') : rec.transcript ? undefined : t('conversation.needsTranscript')}
            onClick={() =>
              act(() => api.resummarize(rec.id), rec.summary?.editedAt ? t('editor.regenerateEditedConfirm') : t('details.regenerateConfirm'))
            }
          >
            {t('conversation.resummarize')}
          </button>
          <button
            type="button"
            className="small-button"
            disabled={busy || editing || !rec.audio}
            title={
              editing ? t('conversation.finishEditing') : rec.audio ? t('conversation.retranscribeTitle') : t('conversation.notArchived')
            }
            onClick={() => act(() => api.retranscribe(rec.id), t('conversation.retranscribeConfirm'))}
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

      <div className="card conversation-body" role="tabpanel">
        {tab === 'summary' && editing && rec.summary && (
          <Suspense fallback={<p className="muted">{t('editor.loading')}</p>}>
            <SummaryEditor
              recordingId={rec.id}
              title={rec.summary.title}
              markdown={rec.summary.markdown ?? ''}
              onSaved={setRec}
              onClose={() => setEditing(false)}
            />
          </Suspense>
        )}

        {tab === 'summary' && !editing && (
          <>
            <div className="summary-head">
              <h2>{t('conversation.summaryHeading')}</h2>
              <div className="summary-tools">
                {rec.transcript && <SummaryDetails rec={rec} onRegenerated={load} />}
                {rec.summary && (
                  <button type="button" className="ghost-button" onClick={() => setEditing(true)}>
                    {t('editor.edit')}
                  </button>
                )}
                {rec.summary?.markdown && (
                  <CopyButton className="ghost-button" text={`# ${rec.summary.title}\n\n${rec.summary.markdown}`} label={t('conversation.copySummary')} />
                )}
              </div>
            </div>
            {rec.summary?.markdown ? (
              <div className="prose">
                <Markdown text={rec.summary.markdown} />
                <p className="model-note">
                  {rec.summary.model && t('conversation.summarizedWith', { model: rec.summary.model })}
                  {rec.summary.editedAt && (
                    <>
                      {rec.summary.model && ' · '}
                      {t('editor.edited', { date: formatDate(rec.summary.editedAt, { dateStyle: 'medium', timeStyle: 'short' }) })}
                    </>
                  )}
                </p>
              </div>
            ) : (
              pending(t('conversation.noSummary'))
            )}
          </>
        )}

        {tab === 'transcript' &&
          (rec.transcript ? (
            <>
              {rec.transcript.text ? <Transcript text={rec.transcript.text} /> : <p className="muted">{t('conversation.noSpeech')}</p>}
              <p className="model-note">{t('conversation.transcribedWith', { model: rec.transcript.model })}</p>
            </>
          ) : (
            pending(t('conversation.noTranscript'))
          ))}

        {tab === 'source' &&
          (rec.audio ? (
            <div className="source">
              <audio controls preload="metadata" src={api.audioURL(rec.id)}>
                {t('conversation.noAudioSupport')}
              </audio>
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
    </section>
  );
}
