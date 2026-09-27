import { lazy, ReactNode, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { api, Recording } from '../api/client';
import { purgeDate } from '../lib/trash';
import { Board, boardLanes } from '../components/Board';
import { CopyButton } from '../components/CopyButton';
import { BackIcon, CalendarIcon, CopyIcon, DownloadIcon, NewNoteIcon, RetranscribeIcon, TrashIcon } from '../components/Icons';
import { inline, Markdown } from '../components/Markdown';
import { NoteDone, NoteLabels } from '../components/Labels';
import { ActionItems } from '../components/ActionItems';
import { PriorityFlag, TaskControls } from '../components/TaskControls';
import { TimeControls } from '../components/TimeControls';
import { useTaskParse } from '../lib/useTaskParse';
import { formatDue, formatRepeat } from '../lib/tasks';
import { MoveToFolder } from '../components/MoveToFolder';
import { ShareNote } from '../components/ShareNote';
import { SubNotes } from '../components/SubNotes';
import { SummaryDetails } from '../components/SummaryDetails';
import { useNotes } from '../context/NotesContext';
import { Sync, useAutosave } from '../hooks/useAutosave';
import { locale } from '../i18n';
import { errorText } from '../lib/errors';
import { folderPath, notePath } from '../lib/folders';
import { noteRefPath } from '../lib/noteRefs';
import { formatBytes, formatClock, formatDate, formatDuration, noteType, processing, statusLabel, title as titleOf, when } from '../lib/recordings';

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

// SyncState is a colored dot before the note's number under its title: green when all is saved,
// amber for unsaved changes, pulsing while saving, red when a save failed (click to retry).
// The words are its tooltip and are read out by screen readers.
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
            : sync === 'conflict'
              ? t('editor.sync.conflict')
              : t('editor.sync.error', { error: error ?? '' });
  const failed = sync === 'error' || sync === 'offline';
  return (
    <>
      {failed ? (
        <button type="button" className={`sync-state ${sync}`} title={`${text} ${t('editor.sync.retryHint')}`} aria-label={`${text} ${t('editor.sync.retry')}`} onClick={onRetry}>
          <span className="sync-dot" aria-hidden="true" />
        </button>
      ) : (
        <span className={`sync-state ${sync}`} title={text}>
          <span className="sync-dot" aria-hidden="true" />
        </span>
      )}
      <span className="sr-only" role="status" aria-live="polite">
        {text}
      </span>
    </>
  );
}

interface BodyProps {
  rec: Recording;
  aiReady: boolean;
  tab: Tab;
  setTab: (t: Tab) => void;
  setRec: (r: Recording) => void;
  reload: () => Promise<void>;
  // created is set for a note that was just made; its title is selected for typing.
  created: boolean;
  // restart shows the note afresh, with the text as stored (e.g. after someone else changed it).
  restart: () => void;
}

// NoteBody is a note's page below the back link. It is re-created when a new summary
// arrives (see the key in Conversation), so the editor always starts from the stored text.
// Text notes show only their text (kept as the summary): no transcript, source or AI actions.
function NoteBody({ rec, aiReady, tab, setTab, setRec, reload, created, restart }: BodyProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const notes = useNotes();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const summary = rec.summary;
  const editable = !!summary;
  // A note shared for viewing is read-only; only its owner shares, deletes and reprocesses it.
  const access = rec.access ?? 'owner';
  const readOnly = access === 'viewer';
  const isOwner = access === 'owner';
  const audio = useRef<HTMLAudioElement>(null);
  const [seek, setSeek] = useState<number | null>(null);
  const [durationMs, setDurationMs] = useState(rec.format?.durationMs ?? 0);
  const highlights = rec.highlights ?? [];
  const isText = noteType(rec) === 'text';
  const isDocument = noteType(rec) === 'document';
  const isBoard = noteType(rec) === 'board';
  const titleInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (created) titleInput.current?.select();
  }, [created]);
  // A date typed into the title ("Call Anna tomorrow 3pm") is offered as the task's date
  // while the title is being edited; Enter takes it out of the title and sets it.
  const [titleTyped, setTitleTyped] = useState(false);

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
  const autosave = useAutosave(rec.id, summary?.title ?? titleOf(rec), rec.revision, setRec);
  // Someone else changed the text: show their version, unless there are unsaved changes
  // here; then the user decides which one stays. A save under way may be what changed it.
  const { known, dirty, settled, conflict } = autosave;
  useEffect(() => {
    let stale = false;
    void settled().then(() => {
      if (stale || known(rec.revision)) return;
      if (dirty()) conflict();
      else restart();
    });
    return () => {
      stale = true;
    };
  }, [rec.revision, known, dirty, settled, conflict, restart]);
  // A date typed into the title is offered as the note's due date.
  const { parsed: typedDate, onKeyDown: titleKey } = useTaskParse(autosave.title, titleTyped && !isBoard);
  // A board has no text; only its title is saved like a summary's.
  const { editorReady } = autosave;
  useEffect(() => {
    if (isBoard) editorReady(() => '');
  }, [isBoard, editorReady]);

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

  // createSub makes an empty text note under this one and opens it, ready to type its title.
  async function createSub() {
    setBusy(true);
    setError(null);
    try {
      const sub = await api.createTextNote(t('conversations.untitled'), '', rec.id);
      notes.upsert(sub);
      navigate(`/conversations/${sub.id}`, { state: { created: true } });
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

  // Deleting moves the note to the trash, where it can be restored for TRASH_DAYS.
  async function handleDelete() {
    setBusy(true);
    try {
      // Pending edits go with it, so they are there when it is restored.
      await autosave.save();
      await notes.moveToTrash(rec);
      autosave.discard();
      navigate('/', { replace: true });
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  async function handleRestore() {
    setBusy(true);
    setError(null);
    try {
      setRec(await notes.restore(rec));
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  async function handleDeleteForever() {
    const confirmKey = isBoard ? 'conversation.deleteBoardConfirm' : isDocument ? 'conversation.deleteDocumentConfirm' : isText ? 'conversation.deleteTextConfirm' : 'conversation.deleteConfirm';
    if (!window.confirm(t(confirmKey, { title: autosave.title || titleOf(rec) }))) return;
    setBusy(true);
    try {
      autosave.discard();
      await notes.deleteForever(rec);
      navigate('/', { replace: true });
    } catch (err) {
      setError(errorText(err, t));
      setBusy(false);
    }
  }

  // The toolbar's download and copy act on the shown tab.
  const download =
    tab === 'summary' && summary?.markdown
      ? { href: api.downloadURL(rec.id, 'summary'), label: t(isText ? 'conversation.downloadText' : 'conversation.downloadSummary') }
      : tab === 'transcript' && rec.transcript?.text
        ? { href: api.downloadURL(rec.id, 'transcript'), label: t(isDocument ? 'conversation.downloadDocumentText' : 'conversation.downloadTranscript') }
        : tab === 'source' && rec.audio
          ? { href: api.audioURL(rec.id, true), label: t('conversation.downloadAudio') }
          : tab === 'source' && rec.file
            ? { href: api.fileURL(rec.id, true), label: t('conversation.downloadDocument') }
            : null;
  const copy =
    tab === 'summary' && summary?.markdown
      ? { text: `# ${autosave.title}\n\n${summary.markdown}`, label: t(isText ? 'conversation.copyText' : 'conversation.copySummary') }
      : tab === 'transcript' && rec.transcript?.text
        ? { text: rec.transcript.text, label: t(isDocument ? 'conversation.copyDocumentText' : 'conversation.copyTranscript') }
        : null;

  const titleDate = typedDate && (typedDate.due || typedDate.priority) && typedDate.title ? typedDate : null;
  async function applyTitleDate() {
    if (!titleDate) return;
    setTitleTyped(false);
    autosave.setTitle(titleDate.title);
    setError(null);
    try {
      // The shorter title is saved first, so the date isn't saved over by it or vice versa.
      await autosave.save();
      let r = rec;
      if (titleDate.due) r = await api.setNoteDue(rec.id, { remind: rec.due?.remind ?? 0, ...titleDate.due });
      if (titleDate.priority) r = await api.setNotePriority(rec.id, titleDate.priority);
      setRec(r);
    } catch (err) {
      setError(errorText(err, t));
    }
  }

  const state = statusLabel(rec, aiReady);
  // Where the note is: the folders above it, then the notes it is a sub-note of.
  const parents = notePath(rec, notes.recordings);
  const folder = folderPath((parents[0] ?? rec).folderId, notes.folders);
  // The boards showing the note, with the lane it is in on each.
  const lanes = boardLanes(rec, notes.recordings ?? [], { folders: notes.folders ?? [], filters: notes.filters ?? [], filterContext: notes.filterContext });
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
  const sourceBadge = isText
    ? t('conversations.types.text')
    : isBoard
      ? t('conversations.types.board')
      : rec.source === 'pocket'
      ? t('conversation.sourcePocket')
      : rec.source === 'upload'
        ? t('conversation.sourceUpload')
        : rec.source === 'remarkable'
          ? t('conversation.sourceRemarkable')
          : '';
  // Documents name their tabs and actions after pages instead of audio.
  const tabLabel = (id: Tab) => (isDocument && id !== 'summary' ? t(`conversation.documentTabs.${id}`) : t(`conversation.tabs.${id}`));
  const canReread = isDocument ? !!rec.file : !!rec.audio;
  // Boards use the whole width; other notes show their labels, date and details in a sidebar
  // when there is room for it (see .note-layout in styles.css).
  const withAside = !isBoard;
  const whenText = (
    <>
      <span className="nowrap">{d.toLocaleDateString(locale(), { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })},</span>{' '}
      <span className="nowrap">{d.toLocaleTimeString(locale(), { hour: 'numeric', minute: '2-digit' })}</span>
    </>
  );
  const location =
    folder.length > 0 || parents.length > 0 ? (
      <>
        {folder.join(' / ')}
        {parents.map((p, i) => (
          <span key={p.id}>
            {(i > 0 || folder.length > 0) && ' / '}
            <Link to={`/conversations/${p.id}`}>{titleOf(p)}</Link>
          </span>
        ))}
      </>
    ) : null;
  const lanePills =
    lanes.length > 0
      ? lanes.map(({ board, lane }) => (
          <Link
            key={board.id}
            to={`/conversations/${board.id}`}
            className="state-pill lane-pill"
            title={t('conversation.laneTitle', { board: titleOf(board), lane })}
            aria-label={t('conversation.laneTitle', { board: titleOf(board), lane })}
          >
            {lane}
          </Link>
        ))
      : null;

  // The note's icon actions: for the shown tab (details, download, copy), then for the whole
  // note. They sit in the header, or at the top of the sidebar when it is shown.
  const tools = (
    <div className="note-tools">
      {tab === 'summary' && rec.transcript && isOwner && <SummaryDetails rec={rec} onRegenerate={(fn) => regenerate(fn)} />}
      {download && (
        <a className="icon-button" href={download.href} download title={download.label} aria-label={download.label}>
          <DownloadIcon />
        </a>
      )}
      {copy && <CopyButton className="icon-button" icon={<CopyIcon />} text={copy.text} label={copy.label} />}
      {!isBoard && <span className="tool-divider" aria-hidden="true" />}
      {!isText && !isBoard && isOwner && (
        <button
          type="button"
          className="icon-button"
          disabled={busy || !canReread}
          title={
            isDocument
              ? canReread
                ? t('conversation.rereadTitle')
                : t('conversation.documentNotStored')
              : canReread
                ? t('conversation.retranscribeTitle')
                : t('conversation.notArchived')
          }
          aria-label={t(isDocument ? 'conversation.reread' : 'conversation.retranscribe')}
          onClick={() =>
            act(
              async () => {
                autosave.discard();
                await api.retranscribe(rec.id);
              },
              t(isDocument ? 'conversation.rereadConfirm' : 'conversation.retranscribeConfirm'),
            )
          }
        >
          <RetranscribeIcon />
        </button>
      )}
      {!readOnly && (
        <button type="button" className="icon-button" disabled={busy} title={t('subNotes.new')} aria-label={t('subNotes.newLabel', { title: titleOf(rec) })} onClick={() => void createSub()}>
          <NewNoteIcon />
        </button>
      )}
      <MoveToFolder rec={rec} setRec={setRec} />
      {!rec.deletedAt && <ShareNote rec={rec} setRec={setRec} />}
      {!rec.deletedAt && !readOnly && (
        <button type="button" className="icon-button danger" disabled={busy} title={t('conversation.moveToTrash')} aria-label={t('conversation.moveToTrash')} onClick={handleDelete}>
          <TrashIcon />
        </button>
      )}
    </div>
  );

  return (
    <div className={withAside ? 'note-layout' : undefined}>
      <div className="note-main">
        {editable && (
          <div className="note-sync">
            <SyncState sync={autosave.sync} error={autosave.error} onRetry={() => void autosave.save()} />
          </div>
        )}
        <div className="conversation-header">
          <div className="title-block">
            {editable ? (
              <input
                ref={titleInput}
                className="title-input"
                aria-label={t('editor.title')}
                maxLength={200}
                readOnly={readOnly}
                value={autosave.title}
                onChange={(e) => {
                  autosave.setTitle(e.target.value);
                  setTitleTyped(true);
                }}
                onBlur={() => setTimeout(() => setTitleTyped(false), 200)}
                onKeyDown={(e) => {
                  if (titleKey(e) || e.key !== 'Enter') return;
                  if (titleDate) {
                    e.preventDefault();
                    void applyTitleDate();
                  }
                  (e.target as HTMLInputElement).blur();
                }}
              />
            ) : (
              <h1>{titleOf(rec)}</h1>
            )}
            {titleDate && (
              <button type="button" className="title-date-hint" onMouseDown={(e) => e.preventDefault()} onClick={() => void applyTitleDate()}>
                <CalendarIcon size={12} />
                {titleDate.due ? t('tasks.titleHint', { when: formatDue(titleDate.due) + (titleDate.due.repeat ? ` · ${formatRepeat(titleDate.due.repeat)}` : '') }) : t('tasks.titleHintPriority')}
                <PriorityFlag priority={titleDate.priority} />
                <kbd>↵</kbd>
              </button>
            )}
          </div>
          {/* The date and labels share a row with the note's icon actions when there is room. */}
          <div className="note-meta-row">
            <p className="conversation-meta muted">
              {rec.number ? <span className="note-number meta-extra">#{rec.number}</span> : null}
              <span className="meta-item meta-extra">{whenText}</span>
              {rec.format?.durationMs ? <span className="meta-item meta-extra">{formatDuration(rec.format.durationMs)}</span> : null}
              {isDocument && rec.pages ? <span className="meta-item meta-extra">{t('conversation.pages', { count: rec.pages })}</span> : null}
              {sourceBadge && <span className="meta-item meta-extra">{sourceBadge}</span>}
              {location && <span className="meta-item meta-extra">{location}</span>}
              {lanePills && <span className="meta-extra">{lanePills}</span>}
              {state && <span className={`state-pill${rec.status === 'failed' ? ' bad' : ''}`}>{state}</span>}
            </p>
            <div className="header-labels">
              <NoteLabels rec={rec} setRec={setRec} />
            </div>
            <div className="header-tools">{tools}</div>
          </div>
        </div>
        {rec.deletedAt && (
          <div className="notice trash-notice">
            <p>{t('conversation.inTrash', { date: formatDate(purgeDate(rec.deletedAt)) })}</p>
            <div className="trash-actions">
              <button type="button" className="pill-button" disabled={busy} onClick={() => void handleRestore()}>
                {t('conversation.restore')}
              </button>
              <button type="button" className="pill-button danger" disabled={busy} onClick={() => void handleDeleteForever()}>
                {t('conversation.deleteForever')}
              </button>
            </div>
          </div>
        )}
        {autosave.sync === 'conflict' && (
          <div className="notice bad conflict-notice" role="alert">
            <p>{t('editor.conflict.text')}</p>
            <div className="trash-actions">
              <button
                type="button"
                className="pill-button"
                onClick={() => {
                  autosave.discard();
                  restart();
                }}
              >
                {t('editor.conflict.theirs')}
              </button>
              <button type="button" className="pill-button" onClick={() => void autosave.keepMine()}>
                {t('editor.conflict.mine')}
              </button>
            </div>
          </div>
        )}
        {readOnly && <p className="notice view-only-notice">{t('sharing.viewOnly')}</p>}
        {error && <p className="error">{error}</p>}

        {!isText && !isBoard && (
          <div className="note-bar">
            <div className="segmented" role="tablist" aria-label={t('conversation.viewLabel')}>
              {TABS.map((id) => (
                <button key={id} type="button" role="tab" aria-selected={tab === id} className={tab === id ? 'active' : ''} onClick={() => setTab(id)}>
                  {tabLabel(id)}
                </button>
              ))}
            </div>
          </div>
        )}

        <div className="conversation-body" role={isText || isBoard ? undefined : 'tabpanel'}>
          {isBoard && <Board rec={rec} setRec={setRec} />}
          {/* The summary stays mounted on other tabs so unsaved edits and the undo history survive. */}
          <div hidden={tab !== 'summary' || isBoard}>
            {isBoard ? null : summary ? (
              <>
                <Suspense
                  fallback={
                    <div className="prose editor-content">
                      <Markdown text={summary.markdown ?? ''} noteLinks />
                    </div>
                  }
                >
                  <SummaryEditor
                    notes={notes.recordings}
                    noteId={rec.id}
                    onOpenNote={(n) => navigate(noteRefPath(n))}
                    markdown={summary.markdown ?? ''}
                    onReady={autosave.editorReady}
                    onChange={autosave.changed}
                    onSaveShortcut={() => void autosave.save()}
                    readOnly={readOnly}
                  />
                </Suspense>
                {!isText && <ActionItems rec={rec} setRec={setRec} />}
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
            isDocument &&
            (rec.transcript ? (
              <>
                {rec.transcript.text ? (
                  <div className="prose">
                    <Markdown text={rec.transcript.text} />
                  </div>
                ) : (
                  <p className="muted">{rec.transcript.model ? t('conversation.noText') : t('conversation.textNotRead')}</p>
                )}
                {rec.transcript.model && <p className="model-note">{t('conversation.readWith', { model: rec.transcript.model })}</p>}
              </>
            ) : (
              pending(t('conversation.noDocumentText'))
            ))}

          {tab === 'transcript' &&
            !isDocument &&
            (rec.transcript ? (
              <>
                {rec.transcript.text ? (
                  <Transcript text={rec.transcript.text} onSeek={seekTo} />
                ) : (
                  <p className="muted">{t('conversation.noSpeech')}</p>
                )}
                <p className="model-note">{t('conversation.transcribedWith', { model: rec.transcript.model })}</p>
              </>
            ) : (
              pending(t('conversation.noTranscript'))
            ))}

          {tab === 'source' &&
            isDocument &&
            (rec.file ? (
              <div className="source">
                {rec.file.contentType === 'application/pdf' ? (
                  <iframe className="document-frame" src={api.fileURL(rec.id)} title={t('conversation.documentFrame', { title: titleOf(rec) })} />
                ) : (
                  <p>
                    <a className="pill-button" href={api.fileURL(rec.id, true)} download>
                      <DownloadIcon /> <span>{t('conversation.downloadDocument')}</span>
                    </a>
                  </p>
                )}
                <dl className="facts">
                  <dt>{t('conversation.file')}</dt>
                  <dd>
                    {rec.file.contentType === 'application/pdf' ? 'PDF' : 'EPUB'} · {formatBytes(rec.file.size)}
                    {rec.pages ? ` · ${t('conversation.pages', { count: rec.pages })}` : ''}
                  </dd>
                  {rec.title && (
                    <>
                      <dt>{t('conversation.remarkableName')}</dt>
                      <dd>{rec.title}</dd>
                    </>
                  )}
                  <dt>{t('conversation.source')}</dt>
                  <dd>{inline(`${t('conversation.remarkableDocument')} \`${rec.recordingId}\``)}</dd>
                </dl>
              </div>
            ) : (
              <p className="muted">{state ?? t('conversation.documentUnavailable')}</p>
            ))}

          {tab === 'source' &&
            !isDocument &&
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
                    {rec.audio.contentType} · {formatBytes(rec.audio.size)}
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
        <SubNotes rec={rec} />
      </div>
      {withAside && (
        <aside className="note-aside" aria-label={t('noteInfo.title')}>
          {tools}
          <section>
            <h2>{t('noteInfo.task')}</h2>
            <fieldset className="note-aside-task view-only-fieldset" disabled={readOnly}>
              <NoteDone rec={rec} setRec={setRec} />
              <TaskControls rec={rec} setRec={setRec} />
            </fieldset>
          </section>
          <section>
            <h2>{t('noteInfo.time')}</h2>
            <TimeControls rec={rec} setRec={setRec} />
          </section>
          <section>
            <h2>{t('labels.title')}</h2>
            <NoteLabels rec={rec} setRec={setRec} withTask={false} />
          </section>
          <section>
            <h2>{t('noteInfo.details')}</h2>
            <dl className="note-facts">
              {rec.number ? (
                <>
                  <dt>{t('noteInfo.number')}</dt>
                  <dd>
                    <span className="note-number">#{rec.number}</span>
                  </dd>
                </>
              ) : null}
              <dt>{t('noteInfo.created')}</dt>
              <dd>{formatDate(d, { dateStyle: 'medium', timeStyle: 'short' })}</dd>
              {sourceBadge && (
                <>
                  <dt>{t('noteInfo.type')}</dt>
                  <dd>{sourceBadge}</dd>
                </>
              )}
              {rec.format?.durationMs ? (
                <>
                  <dt>{t('noteInfo.duration')}</dt>
                  <dd>{formatDuration(rec.format.durationMs)}</dd>
                </>
              ) : null}
              {isDocument && rec.pages ? (
                <>
                  <dt>{t('noteInfo.pages')}</dt>
                  <dd>{t('conversation.pages', { count: rec.pages })}</dd>
                </>
              ) : null}
              {location && (
                <>
                  <dt>{t('noteInfo.location')}</dt>
                  <dd>{location}</dd>
                </>
              )}
              {lanePills && (
                <>
                  <dt>{t('noteInfo.boards')}</dt>
                  <dd className="note-facts-pills">{lanePills}</dd>
                </>
              )}
              {state && (
                <>
                  <dt>{t('noteInfo.status')}</dt>
                  <dd>
                    <span className={`state-pill${rec.status === 'failed' ? ' bad' : ''}`}>{state}</span>
                  </dd>
                </>
              )}
              {summary?.editedAt && (
                <>
                  <dt>{t('noteInfo.edited')}</dt>
                  <dd>{formatDate(summary.editedAt, { dateStyle: 'medium', timeStyle: 'short' })}</dd>
                </>
              )}
              {summary?.model && (
                <>
                  <dt>{t('noteInfo.summaryModel')}</dt>
                  <dd>{summary.model}</dd>
                </>
              )}
            </dl>
          </section>
        </aside>
      )}
    </div>
  );
}

export function Conversation() {
  const { t } = useTranslation();
  const { id = '' } = useParams();
  const notes = useNotes();
  // The note that is open now; answers for a note opened earlier are ignored.
  const openId = useRef(id);
  openId.current = id;
  const [rec, setRecState] = useState<Recording | null>(null);
  // Changes to the open note (saves, processing progress) also update the sidebar. A save
  // of a note left meanwhile (autosave on leaving it) only updates the sidebar.
  const { upsert } = notes;
  const setRec = useCallback(
    (r: Recording) => {
      if (r.id === openId.current) setRecState(r);
      upsert(r);
    },
    [upsert],
  );
  const [aiReady, setAIReady] = useState(true);
  // generation counts the restarts of the open note (see NoteBody's restart).
  const [generation, setGeneration] = useState(0);
  const restart = useCallback(() => setGeneration((g) => g + 1), []);
  const [tab, setTab] = useState<Tab>('summary');
  const [error, setError] = useState<string | null>(null);
  const created = !!(useLocation().state as { created?: boolean } | null)?.created;


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

  // The check mark, labels, folder and parent note can also change in the sidebar; take them over.
  const listed = notes.recordings?.find((r) => r.id === id);
  const listedLabels = listed?.labels?.join(',') ?? '';
  const listedDone = listed?.done ?? false;
  const listedFolder = listed?.folderId ?? '';
  const listedParent = listed?.parentId ?? '';
  const listedTask = JSON.stringify([listed?.due ?? null, listed?.priority ?? 0, listed?.remindAt ?? null]);
  useEffect(() => {
    if (!listed) return;
    setRecState((r) =>
      r &&
      r.id === listed.id &&
      (r.done !== listed.done ||
        (r.labels?.join(',') ?? '') !== listedLabels ||
        (r.folderId ?? '') !== listedFolder ||
        (r.parentId ?? '') !== listedParent ||
        JSON.stringify([r.due ?? null, r.priority ?? 0, r.remindAt ?? null]) !== listedTask)
        ? { ...r, done: listed.done, labels: listed.labels, folderId: listed.folderId, parentId: listed.parentId, due: listed.due, priority: listed.priority, remindAt: listed.remindAt }
        : r,
    );
  }, [listedLabels, listedDone, listedFolder, listedParent, listedTask]);

  // A newer version of the open note arrived in the list (changed by someone else, or in
  // another window): load it in full.
  const listedVersion = listed?.version ?? 0;
  const openVersion = rec?.id === id ? (rec.version ?? 0) : 0;
  useEffect(() => {
    if (openVersion && listedVersion > openVersion) void load();
  }, [listedVersion, openVersion, load]);

  const inProgress = rec ? processing(rec) : false;
  useEffect(() => {
    if (!inProgress) return;
    const timer = setInterval(load, POLL_MS);
    return () => clearInterval(timer);
  }, [inProgress, load]);

  return (
    <section className={`conversation${rec?.type === 'board' ? ' board-note' : rec ? ' with-aside' : ''}`}>
      <Link to="/" className="back-link">
        <BackIcon />
        <span>{t('conversation.back')}</span>
      </Link>
      {rec ? (
        <NoteBody
          key={`${rec.id}:${rec.summary?.createdAt ?? ''}:${generation}`}
          rec={rec}
          aiReady={aiReady}
          tab={tab}
          setTab={setTab}
          setRec={setRec}
          reload={load}
          created={created}
          restart={restart}
        />
      ) : error ? (
        <p className="error">{error}</p>
      ) : (
        <p className="muted">{t('common.loading')}</p>
      )}
    </section>
  );
}
