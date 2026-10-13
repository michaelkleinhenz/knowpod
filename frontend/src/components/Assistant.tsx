import { FormEvent, useEffect, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { api, AssistantAnswer, AssistantSettings as Settings, AssistantUpdate, ModelOption } from '../api/client';
import { errorText } from '../lib/errors';
import { SparkleIcon } from './Icons';
import { Markdown } from './Markdown';
import { ModelPicker } from './OpenRouterSettings';

// useAssistantReady tells whether the user has set up their own AI model; false until known.
export function useAssistantReady(): boolean {
  const [ready, setReady] = useState(false);
  useEffect(() => {
    let stale = false;
    api.assistant().then(
      (s) => !stale && setReady(s.configured),
      () => !stale && setReady(false),
    );
    return () => {
      stale = true;
    };
  }, []);
  return ready;
}

// AssistantSettings sets up the user's own AI model, through their own OpenRouter account:
// it works on notes and writes new ones as asked, with the user's own system prompt.
export function AssistantSettings() {
  const { t } = useTranslation();
  const [current, setCurrent] = useState<Settings | null>(null);
  const [models, setModels] = useState<ModelOption[] | null>(null);
  const [apiKey, setAPIKey] = useState('');
  const [model, setModel] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [webSearch, setWebSearch] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api.assistant().then(
      (s) => {
        setCurrent(s);
        setModel(s.model);
        setSystemPrompt(s.systemPrompt);
        setWebSearch(s.webSearch);
      },
      (e) => setError(errorText(e, t)),
    );
    api.aiModels().then(
      (m) => setModels(m.summary),
      () => setModels([]),
    );
  }, [t]);

  async function save(update: AssistantUpdate) {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      setCurrent(await api.saveAssistant(update));
      setAPIKey('');
      setSaved(true);
    } catch (err) {
      setError(errorText(err, t));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const update: AssistantUpdate = { model, systemPrompt, webSearch };
    if (apiKey.trim()) update.apiKey = apiKey.trim();
    void save(update);
  }

  return (
    <>
      <h2 className="card-title">{t('assistant.settings.title')}</h2>
      <p className="muted">
        <Trans i18nKey="assistant.settings.intro" components={{ 1: <a href="https://openrouter.ai/settings/keys" target="_blank" rel="noreferrer" /> }} />
      </p>
      {current && (
        <p>
          <span className={`status-pill ${current.configured ? 'ok' : 'bad'}`}>
            {current.configured ? t('assistant.settings.active') : t('assistant.settings.notConfigured')}
          </span>
        </p>
      )}
      <form className="form" onSubmit={submit}>
        <label>
          {t('assistant.settings.apiKey')}
          <input
            type="password"
            autoComplete="off"
            placeholder={current?.apiKeyConfigured ? t('settings.ai.apiKeyPlaceholderSet', { hint: current.apiKeyHint ?? '…' }) : 'sk-or-v1-…'}
            value={apiKey}
            onChange={(e) => setAPIKey(e.target.value)}
          />
        </label>
        {current?.apiKeyConfigured && (
          <p className="field-hint">
            <button
              type="button"
              className="link-button danger-link"
              disabled={busy}
              onClick={() => window.confirm(t('assistant.settings.removeKeyConfirm')) && void save({ apiKey: '' })}
            >
              {t('settings.ai.removeKey')}
            </button>
          </p>
        )}

        <ModelPicker
          id="assistant-model"
          label={t('assistant.settings.model')}
          hint={t('assistant.settings.modelHint')}
          models={models}
          value={model}
          audio={false}
          onChange={setModel}
        />

        <label>
          {t('assistant.settings.systemPrompt')}
          <textarea
            rows={6}
            maxLength={8000}
            value={systemPrompt}
            placeholder={t('assistant.settings.systemPromptPlaceholder')}
            onChange={(e) => setSystemPrompt(e.target.value)}
          />
        </label>
        <p className="muted field-hint">{t('assistant.settings.systemPromptHint')}</p>

        <label className="checkbox">
          <input type="checkbox" checked={webSearch} onChange={(e) => setWebSearch(e.target.checked)} />
          {t('assistant.settings.webSearch')}
        </label>
        <p className="muted field-hint">{t('assistant.settings.webSearchHint')}</p>

        {error && <p className="error">{error}</p>}
        {saved && <p className="success">{t('settings.ai.saved')}</p>}
        <button type="submit" disabled={busy}>
          {busy ? t('common.saving') : t('common.save')}
        </button>
      </form>
    </>
  );
}

// useAssistantRun runs a request to the user's AI model, keeping the latest answer; answers
// of a request that was cancelled or replaced are ignored.
function useAssistantRun() {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [answer, setAnswer] = useState<AssistantAnswer | null>(null);
  const request = useRef(0);

  async function run(call: () => Promise<AssistantAnswer>) {
    const id = ++request.current;
    setBusy(true);
    setError(null);
    setAnswer(null);
    try {
      const out = await call();
      if (id === request.current) setAnswer(out);
    } catch (err) {
      if (id === request.current) setError(errorText(err, t));
    } finally {
      if (id === request.current) setBusy(false);
    }
  }
  function cancel() {
    request.current++;
    setBusy(false);
  }
  return { busy, error, setError, answer, setAnswer, run, cancel };
}

// AssistantDialog is the frame of the assistant's dialogs: the request, the answer and what
// to do with it. Escape or a click beside it closes it.
function AssistantDialog(props: {
  heading: string;
  hint: string;
  placeholder: string;
  prompt: string;
  setPrompt: (p: string) => void;
  state: ReturnType<typeof useAssistantRun>;
  onRun: () => void;
  onClose: () => void;
  applyLabel: string;
  onApply: () => void;
  applying?: boolean;
}) {
  const { t } = useTranslation();
  const { heading, hint, placeholder, prompt, setPrompt, state, onRun, onClose, applyLabel, onApply, applying } = props;
  const input = useRef<HTMLTextAreaElement>(null);
  useEffect(() => input.current?.focus(), []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      e.stopPropagation();
      state.cancel();
      onClose();
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [onClose, state]);

  function submit(e: FormEvent) {
    e.preventDefault();
    if (prompt.trim() && !state.busy) onRun();
  }

  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && !state.busy && !state.answer && onClose()}>
      <div className="new-item-dialog assistant-dialog" role="dialog" aria-modal="true" aria-label={heading}>
        <h2>
          <SparkleIcon size={18} /> {heading}
        </h2>
        <p className="muted assistant-hint">{hint}</p>
        <form className="assistant-ask" onSubmit={submit}>
          <textarea
            ref={input}
            rows={3}
            maxLength={4000}
            value={prompt}
            disabled={state.busy || applying}
            placeholder={placeholder}
            aria-label={placeholder}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              // Ctrl/Cmd+Enter sends the request.
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit(e);
            }}
          />
          <div className="new-item-actions">
            <button type="submit" className="pill-button primary" disabled={state.busy || applying || !prompt.trim()}>
              {state.answer ? t('assistant.again') : t('assistant.run')}
            </button>
          </div>
        </form>

        {state.busy && (
          <div className="ai-status" role="status">
            <span className="ai-spinner" aria-hidden="true" />
            {t('assistant.working')}
            <button type="button" className="pill-button" onClick={state.cancel}>
              {t('editor.ai.cancel')}
            </button>
          </div>
        )}
        {state.error && (
          <p className="error" role="alert">
            {state.error}
          </p>
        )}

        {state.answer && (
          <>
            <div className="prose ai-preview assistant-preview">
              {state.answer.title && <h1>{state.answer.title}</h1>}
              <Markdown text={state.answer.markdown} />
            </div>
            <p className="muted ai-model">{t('editor.ai.model', { model: state.answer.model })}</p>
          </>
        )}

        <div className="new-item-actions">
          <button
            type="button"
            className="pill-button"
            onClick={() => {
              state.cancel();
              onClose();
            }}
          >
            {state.answer ? t('editor.ai.discard') : t('nav.cancel')}
          </button>
          {state.answer && (
            <button type="button" className="pill-button primary" disabled={applying} onClick={onApply}>
              {applyLabel}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// AssistantEditDialog has the user's AI model work on the open note as asked: the answer,
// the note's new text, is shown first and replaces the text when the user takes it.
export function AssistantEditDialog({ title, markdown, onApply, onClose }: { title: string; markdown: () => string; onApply: (markdown: string) => void; onClose: () => void }) {
  const { t } = useTranslation();
  const [prompt, setPrompt] = useState('');
  const state = useAssistantRun();
  return (
    <AssistantDialog
      heading={t('assistant.edit.title')}
      hint={t('assistant.edit.hint')}
      placeholder={t('assistant.edit.placeholder')}
      prompt={prompt}
      setPrompt={setPrompt}
      state={state}
      onRun={() => void state.run(() => api.assistantEdit(title, markdown(), prompt.trim()))}
      onClose={onClose}
      applyLabel={t('assistant.edit.apply')}
      onApply={() => {
        if (!state.answer) return;
        onApply(state.answer.markdown);
        onClose();
      }}
    />
  );
}

// AssistantCreateDialog has the user's AI model write a new note as asked; onCreate saves it
// when the user takes it.
export function AssistantCreateDialog({ onCreate, onClose }: { onCreate: (title: string, markdown: string) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation();
  const [prompt, setPrompt] = useState('');
  const [applying, setApplying] = useState(false);
  const state = useAssistantRun();

  async function create() {
    const a = state.answer;
    if (!a) return;
    setApplying(true);
    try {
      await onCreate(a.title || t('conversations.untitled'), a.markdown);
    } catch (err) {
      state.setError(errorText(err, t));
      setApplying(false);
    }
  }

  return (
    <AssistantDialog
      heading={t('assistant.create.title')}
      hint={t('assistant.create.hint')}
      placeholder={t('assistant.create.placeholder')}
      prompt={prompt}
      setPrompt={setPrompt}
      state={state}
      onRun={() => void state.run(() => api.assistantCreate(prompt.trim()))}
      onClose={onClose}
      applyLabel={t('assistant.create.apply')}
      onApply={() => void create()}
      applying={applying}
    />
  );
}
