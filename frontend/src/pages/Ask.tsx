import { FormEvent, KeyboardEvent, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import { api, AskAnswer, AskSource, AskTurn } from '../api/client';
import { NoteIcon, SparkleIcon } from '../components/Icons';
import { Markdown } from '../components/Markdown';
import { errorText } from '../lib/errors';
import { useNotes } from '../context/NotesContext';
import { formatClock, formatDate, iconKind } from '../lib/recordings';

// Turn is one question of the conversation and its answer (or why there is none).
interface Turn {
  id: number;
  question: string;
  answer?: AskAnswer;
  error?: string;
}

// The conversation is kept for the browser tab, so it is still there after opening a source.
const STORE_KEY = 'knowpod.ask';
// HISTORY is how many earlier questions go along with a follow-up.
const HISTORY = 3;

function loadTurns(): Turn[] {
  try {
    const raw = sessionStorage.getItem(STORE_KEY);
    const list = raw ? (JSON.parse(raw) as Turn[]) : [];
    return Array.isArray(list) ? list.filter((t) => t && typeof t.question === 'string') : [];
  } catch {
    return [];
  }
}

function saveTurns(turns: Turn[]) {
  try {
    sessionStorage.setItem(STORE_KEY, JSON.stringify(turns.filter((t) => t.answer || t.error)));
  } catch {
    // Not kept; the conversation is lost when the page is left.
  }
}

// sourcePath opens a source: a recording at the moment the quote is said.
function sourcePath(s: AskSource): string {
  return `/conversations/${encodeURIComponent(s.id)}${s.offsetMs !== undefined ? `?t=${s.offsetMs}` : ''}`;
}

function Sources({ sources }: { sources: AskSource[] }) {
  const { t } = useTranslation();
  const { recordings } = useNotes();
  // A note in the list shows its own icon (a photo, a PDF); others show their type's.
  const icon = (s: AskSource) => {
    const r = recordings?.find((n) => n.id === s.id);
    return r ? iconKind(r) : s.type;
  };
  if (sources.length === 0) return null;
  return (
    <ol className="ask-sources" aria-label={t('ask.sources')}>
      {sources.map((s) => (
        <li key={s.ref} id={`source-${s.ref}`}>
          <span className="ask-ref">{s.ref}</span>
          <NoteIcon type={icon(s)} />
          <div className="ask-source-body">
            <Link to={sourcePath(s)} className="ask-source-title">
              {s.title}
            </Link>
            <span className="muted ask-source-meta">
              {s.number ? `#${s.number} · ` : ''}
              {formatDate(s.date, { dateStyle: 'medium' })}
              {s.offsetMs !== undefined && (
                <>
                  {' · '}
                  <Link to={sourcePath(s)}>{t('ask.playAt', { time: formatClock(s.offsetMs) })}</Link>
                </>
              )}
            </span>
            {s.quote && <q className="ask-quote">{s.quote}</q>}
          </div>
        </li>
      ))}
    </ol>
  );
}

// Answer shows an answer with its citations linked to the notes, then the notes.
function Answer({ answer }: { answer: AskAnswer }) {
  const { t } = useTranslation();
  const bySource = new Map(answer.sources.map((s) => [s.ref, s]));
  if (!answer.answer) return <p className="muted">{t('ask.nothingFound')}</p>;
  return (
    <>
      <div className="prose ask-answer">
        <Markdown
          text={answer.answer}
          noteLinks
          cite={(n) => {
            const s = bySource.get(n);
            return s ? (
              <Link className="ask-cite" to={sourcePath(s)} title={s.title} aria-label={t('ask.citeLabel', { n, title: s.title })}>
                {n}
              </Link>
            ) : (
              <></>
            );
          }}
        />
      </div>
      <Sources sources={answer.sources} />
      {answer.model && <p className="model-note">{t('ask.answeredWith', { model: answer.model })}</p>}
    </>
  );
}

// Ask answers questions about the user's notes: an AI model finds the notes about the
// question and answers from them, citing each. Follow-up questions keep the context.
export function Ask() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const [turns, setTurns] = useState<Turn[]>(loadTurns);
  const [question, setQuestion] = useState('');
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLTextAreaElement>(null);
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => saveTurns(turns), [turns]);
  useEffect(() => {
    end.current?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' });
  }, [turns.length, busy]);

  const ask = useCallback(
    async (q: string) => {
      q = q.trim();
      if (!q || busy) return;
      const id = Date.now();
      const history: AskTurn[] = turns
        .filter((x) => x.answer?.answer)
        .slice(-HISTORY)
        .map((x) => ({ question: x.question, answer: x.answer!.answer }));
      setTurns((list) => [...list, { id, question: q }]);
      setQuestion('');
      setBusy(true);
      try {
        const answer = await api.ask(q, history);
        setTurns((list) => list.map((x) => (x.id === id ? { ...x, answer } : x)));
      } catch (err) {
        setTurns((list) => list.map((x) => (x.id === id ? { ...x, error: errorText(err, t) } : x)));
      } finally {
        setBusy(false);
        input.current?.focus();
      }
    },
    [busy, turns, t],
  );

  // A question handed over in the address (from the search box) is asked right away.
  const handed = params.get('q');
  useEffect(() => {
    if (!handed) return;
    setParams({}, { replace: true });
    void ask(handed);
    // Only when a question is handed over, not whenever ask changes.
  }, [handed]); // eslint-disable-line react-hooks/exhaustive-deps

  function submit(e: FormEvent) {
    e.preventDefault();
    void ask(question);
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void ask(question);
    }
  }

  const examples = [t('ask.example1'), t('ask.example2'), t('ask.example3')];
  return (
    <div className="ask-page">
      <div className="time-log-head ask-head">
        <h1>
          <SparkleIcon size={22} /> {t('ask.title')}
        </h1>
        {turns.length > 0 && (
          <button type="button" className="pill-button" disabled={busy} onClick={() => setTurns([])}>
            {t('ask.newConversation')}
          </button>
        )}
      </div>

      {turns.length === 0 && (
        <div className="ask-intro">
          <p className="muted">{t('ask.intro')}</p>
          <div className="ask-examples">
            {examples.map((ex) => (
              <button key={ex} type="button" className="pill-button" onClick={() => void ask(ex)}>
                {ex}
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="ask-thread" aria-live="polite">
        {turns.map((turn) => (
          <section key={turn.id} className="ask-turn">
            <p className="ask-question">{turn.question}</p>
            {turn.answer ? (
              <Answer answer={turn.answer} />
            ) : turn.error ? (
              <p className="error">{turn.error}</p>
            ) : (
              <p className="muted ask-pending">
                <span className="ask-spinner" aria-hidden="true" /> {t('ask.searching')}
              </p>
            )}
          </section>
        ))}
        <div ref={end} />
      </div>

      <form className="ask-form" onSubmit={submit}>
        <textarea
          ref={input}
          rows={2}
          maxLength={1000}
          value={question}
          placeholder={turns.length ? t('ask.followUpPlaceholder') : t('ask.placeholder')}
          aria-label={t('ask.question')}
          onChange={(e) => setQuestion(e.target.value)}
          onKeyDown={onKeyDown}
          autoFocus
        />
        <button type="submit" disabled={busy || !question.trim()}>
          {busy ? t('ask.asking') : t('ask.submit')}
        </button>
      </form>
    </div>
  );
}
